// Package log — zap adapter for Kratos log.Logger interface.
//
// Kratos log.Logger 接口签名:
//
//	Log(level Level, keyvals ...interface{}) error
//
// 本文件把 zap.Logger 适配成 Kratos log.Logger,这样 Kratos middleware / Helper / WithContext
// 都能直接用 zap 做底层。
package log

import (
	"fmt"

	klog "github.com/go-kratos/kratos/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ZapLogger 是 Kratos log.Logger interface 的 zap 实现。
type ZapLogger struct {
	zl *zap.Logger
}

// NewZapLogger 用一个已构造好的 zap.Logger 包装成 Kratos log.Logger。
func NewZapLogger(zl *zap.Logger) klog.Logger {
	return &ZapLogger{zl: zl}
}

// Log 实现 klog.Logger 接口。
//
// Kratos Helper 把 (level, key1, val1, key2, val2, ...) 这种 kv 数组传进来,
// 我们把它转成 zap.Field 调 zap 输出。
func (z *ZapLogger) Log(level klog.Level, keyvals ...interface{}) error {
	if len(keyvals) == 0 {
		return nil
	}
	if len(keyvals)%2 != 0 {
		// kv 不成对,补一个 nil 防 panic
		keyvals = append(keyvals, "MISSING_VALUE")
	}

	// 拼成 zap.Field
	fields := make([]zap.Field, 0, len(keyvals)/2)
	for i := 0; i < len(keyvals); i += 2 {
		k := fmt.Sprintf("%v", keyvals[i])
		fields = append(fields, zap.Any(k, keyvals[i+1]))
	}

	switch level {
	case klog.LevelDebug:
		z.zl.Debug("", fields...)
	case klog.LevelInfo:
		z.zl.Info("", fields...)
	case klog.LevelWarn:
		z.zl.Warn("", fields...)
	case klog.LevelError:
		z.zl.Error("", fields...)
	case klog.LevelFatal:
		z.zl.Fatal("", fields...)
	default:
		z.zl.Info("", fields...)
	}
	return nil
}

// Sync 在进程退出前调用,把 buffer 刷到 stdout。
func (z *ZapLogger) Sync() error {
	// zap.Sync 在 stdout 上可能返回 "sync /dev/stdout: invalid argument"(已知问题)
	// 忽略这个错误,其它正常返回
	if err := z.zl.Sync(); err != nil {
		if isStdoutSyncErr(err) {
			return nil
		}
		return err
	}
	return nil
}

func isStdoutSyncErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "sync /dev/stdout: invalid argument" ||
		msg == "sync /dev/stdout: inappropriate ioctl for device" ||
		msg == "sync /dev/stdout: The handle is invalid."
}

// 编译期 interface 检查
var _ klog.Logger = (*ZapLogger)(nil)

// 一个开发期辅助:用 zap.NewDevelopment() 风格替换默认 logger(彩色 console 输出)。
// 生产代码不用调用,Setup 已经做了 production JSON 配置。
func MustNewDevelopmentLogger() klog.Logger {
	zl, err := zap.NewDevelopment(
		zap.AddCaller(),
		zap.AddCallerSkip(3),
	)
	if err != nil {
		panic(err)
	}
	_ = zapcore.WriteSyncer(nil) // 引入 zapcore 避免 unused import 警告
	return NewZapLogger(zl)
}
