package storage

import "testing"

func TestGroupSelectionGate(t *testing.T) {
	for _, role := range []string{"guest", "member", "admin"} {
		for _, allow := range []bool{false, true} {
			for _, access := range []string{"admin", "members", "public"} {
				t.Run(role+access+map[bool]string{true: " selectable", false: " fixed"}[allow], func(t *testing.T) {
					target := &Target{ID: 9, Enabled: true, Configured: true, UploadAccess: access}
					logged, admin := role != "guest", role == "admin"
					if !CanSelectTarget(target, 1, "target:9", logged, admin, allow) {
						t.Fatal("assigned default rejected")
					}
					want := allow && (admin || access == "public" || logged && access == "members")
					if got := CanSelectTarget(target, 1, "target:2", logged, admin, allow); got != want {
						t.Fatalf("got %v want %v", got, want)
					}
				})
			}
		}
	}
}

func TestUploadTargetAccess(t *testing.T) {
	for _, tc := range []struct {
		name, access, policy                                  string
		loggedIn, admin, enabled, configured, isDefault, want bool
	}{
		{"admin can choose", "admin", "default", true, true, true, true, false, true},
		{"member cannot choose private", "admin", "default", true, false, true, true, false, false},
		{"member chooses member target", "members", "default", true, false, true, true, false, true},
		{"guest cannot choose member target", "members", "default", false, false, true, true, false, false},
		{"guest chooses public", "public", "default", false, false, true, true, false, true},
		{"guest uses default", "admin", "default", false, false, true, true, true, true},
		{"fixed target", "admin", "target:9", true, false, true, true, false, true},
		{"fixed group cannot override", "public", "target:8", true, false, true, true, false, false},
		{"fixed mirror cannot override", "public", "mirror", true, false, true, true, false, false},
		{"disabled blocked even admin", "public", "default", true, true, false, true, true, false},
		{"unconfigured blocked", "public", "default", true, true, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := &Target{ID: 9, UploadAccess: tc.access, Enabled: tc.enabled, Configured: tc.configured}
			var defaultID uint
			if tc.isDefault {
				defaultID = 9
			}
			if got := CanSelectTarget(target, defaultID, tc.policy, tc.loggedIn, tc.admin, tc.policy == "default" || tc.policy == ""); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	if !CanSelectTarget(&Target{ID: 1, LegacyMode: "local", Enabled: true, Configured: true}, 2, "local", true, false, false) {
		t.Fatal("legacy fixed policy lost")
	}
	for _, value := range []string{"target:0", "target:-1", "target:x", "s3", "target:18446744073709551616"} {
		if _, err := ParseTargetSelection(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if id, err := ParseTargetSelection("target:9"); err != nil || id != 9 {
		t.Fatal("valid target rejected")
	}
}
