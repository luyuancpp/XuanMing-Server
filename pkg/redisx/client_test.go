package redisx

import (
	"testing"

	"github.com/redis/go-redis/v9/maintnotifications"
)

func TestResolveMaintMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want maintnotifications.Mode
	}{
		{"empty falls back to disabled", "", maintnotifications.ModeDisabled},
		{"explicit disabled", "disabled", maintnotifications.ModeDisabled},
		{"explicit auto", "auto", maintnotifications.ModeAuto},
		{"explicit enabled", "enabled", maintnotifications.ModeEnabled},
		{"invalid falls back to disabled", "garbage", maintnotifications.ModeDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMaintMode(tc.in); got != tc.want {
				t.Fatalf("resolveMaintMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultMaintModeIsDisabled(t *testing.T) {
	if DefaultMaintNotificationsMode != maintnotifications.ModeDisabled {
		t.Fatalf("default maint mode = %q, want disabled (自建 Redis 默认应关闭探测)", DefaultMaintNotificationsMode)
	}
}
