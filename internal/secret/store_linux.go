//go:build linux

package secret

// New returns the Linux build-time key store implementation. The designed
// store is the current user's Secret Service (docs/v1-scheme.md §6.2);
// until that implementation lands, this build explicitly refuses every
// secret operation instead of falling back to plaintext.
func New(dataDir string) Store {
	return &unavailableStore{
		platform: "linux-secret-service",
		hint:     "ai-gateway 在 Linux 上使用当前用户的 Secret Service 保存钥匙，该实现尚未落地，本构建明确不支持系统钥匙存储；需要钥匙的 provider 无法启动或写入，绝不会回退为明文存储",
	}
}
