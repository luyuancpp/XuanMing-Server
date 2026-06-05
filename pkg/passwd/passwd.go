// Package passwd 提供密码哈希工具(bcrypt)。
//
// 设计:
//   - 客户端传 SHA-256 摘要(避免明文密码上行),服务端 bcrypt(摘要) 落库
//   - 校验:bcrypt.CompareHashAndPassword
//   - dev 默认 cost=4 加快本地登录;prod 配置成 10+
//
// 跟 pkg/auth(JWT) 同域但分包:JWT 是 token 签发,passwd 是密码哈希,职责互不相干。
package passwd

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// 推荐 bcrypt cost:开发期 4,生产 10+。
const (
	DevCost  = 4
	ProdCost = 10
)

// ErrMismatch 密码不匹配(bcrypt 内部错误统一翻译)。
var ErrMismatch = errors.New("passwd: hash mismatch")

// Hash 用指定 cost 对客户端密码摘要做 bcrypt。
//
// cost 范围 4-31,推荐 10。<4 或 >31 时强制取 DevCost。
func Hash(clientDigest string, cost int) (string, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = DevCost
	}
	b, err := bcrypt.GenerateFromPassword([]byte(clientDigest), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify 比较 bcrypt 哈希与客户端摘要。匹配返回 nil,不匹配返回 ErrMismatch。
//
// 其它错误(哈希格式坏)直接透传。
func Verify(stored, clientDigest string) error {
	err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(clientDigest))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrMismatch
	}
	return err
}
