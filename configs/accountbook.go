package configs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AccountBook 本机账号簿：保存多个校园网账号，密码以 AES-256-GCM 加密存储。
// 密钥保存在同目录 .secret.key（0600），账号簿文件 accounts.json（0600）。
// 加密防的是凭据明文落盘、被备份/网盘同步带走的场景；
// 对拥有相同用户权限的本地进程，文件权限本身才是边界。

const accountFileName = "accounts.json"

// ErrAccountNotFound 表示账号簿中不存在该用户名
var ErrAccountNotFound = errors.New("账号簿中不存在该账号")

type accountEntry struct {
	Username string `json:"username"`
	Password string `json:"password"` // base64(nonce || AES-GCM 密文)
}

type accountFile struct {
	Version  int            `json:"version"`
	Accounts []accountEntry `json:"accounts"`
}

// AccountBook 多账号存储，所有方法并发安全
type AccountBook struct {
	mu       sync.Mutex
	dir      string
	key      []byte
	accounts []accountEntry
}

// OpenAccountBook 打开默认位置的账号簿（~/.config/xtu-connect/accounts.json），
// 文件或密钥不存在时自动创建
func OpenAccountBook() (*AccountBook, error) {
	path := DefaultPath()
	if path == "" {
		return nil, errors.New("cannot determine home directory")
	}
	return openAccountBookAt(filepath.Dir(path))
}

func openAccountBookAt(dir string) (*AccountBook, error) {
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return nil, err
	}
	b := &AccountBook{dir: dir, key: key}
	data, err := os.ReadFile(filepath.Join(dir, accountFileName))
	if err == nil {
		var af accountFile
		if json.Unmarshal(data, &af) == nil && af.Version == 1 {
			b.accounts = af.Accounts
		}
		// 解析失败视为空账号簿：密钥更换后旧数据本就无法解密，
		// 保留文件反而会让 Add 操作把不可解密的旧条目一直带着
	}
	return b, nil
}

// List 返回全部用户名（顺序与添加一致）
func (b *AccountBook) List() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.accounts))
	for _, a := range b.accounts {
		out = append(out, a.Username)
	}
	return out
}

// Has 报告账号是否存在
func (b *AccountBook) Has(username string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.find(username) >= 0
}

// Add 新增账号；用户名已存在则更新密码（幂等）
func (b *AccountBook) Add(username, password string) error {
	if username == "" || password == "" {
		return errors.New("用户名和密码不能为空")
	}
	enc, err := encryptPassword(b.key, password)
	if err != nil {
		return fmt.Errorf("加密失败: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if i := b.find(username); i >= 0 {
		b.accounts[i].Password = enc
	} else {
		b.accounts = append(b.accounts, accountEntry{Username: username, Password: enc})
	}
	return b.saveLocked()
}

// Remove 删除账号；不存在时返回 ErrAccountNotFound
func (b *AccountBook) Remove(username string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.find(username)
	if i < 0 {
		return ErrAccountNotFound
	}
	b.accounts = append(b.accounts[:i], b.accounts[i+1:]...)
	return b.saveLocked()
}

// Password 返回某账号解密后的密码
func (b *AccountBook) Password(username string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.find(username)
	if i < 0 {
		return "", ErrAccountNotFound
	}
	return decryptPassword(b.key, b.accounts[i].Password)
}

// find 要求已持有 mu
func (b *AccountBook) find(username string) int {
	for i, a := range b.accounts {
		if a.Username == username {
			return i
		}
	}
	return -1
}

// saveLocked 要求已持有 mu；原子写入
func (b *AccountBook) saveLocked() error {
	af := accountFile{Version: 1, Accounts: b.accounts}
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.dir, ".accounts-xtu-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // rename 成功后此调用无效果
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(b.dir, accountFileName))
}

// loadOrCreateKey 读取 32 字节 AES 密钥；不存在则生成并落盘（0600）
func loadOrCreateKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, ".secret.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			return key, nil
		}
		// 密钥文件损坏：重新生成（旧账号簿数据将无法解密，需重新保存）
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptPassword(key []byte, password string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(password), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptPassword(key []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密码数据损坏: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("密码数据损坏")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("密码解密失败（加密密钥已更换，请重新保存该账号）")
	}
	return string(plain), nil
}
