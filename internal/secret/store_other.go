//go:build !windows && !darwin && !linux

package secret

// New returns the build-time key store implementation for platforms without
// a designed system key store. Every operation fails explicitly; ai-gateway
// never falls back to plaintext storage.
func New(dataDir string) Store {
	return &unavailableStore{
		platform: "unsupported-platform",
		hint:     "当前平台没有可用的系统钥匙存储实现；ai-gateway 不会把钥匙写入明文文件",
	}
}
