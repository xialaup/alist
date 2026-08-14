package fastwebdav_crypt

import "testing"

func TestEncryptionForDirDefaultsToRemotePathEncrypted(t *testing.T) {
	d := &FastWebdavCrypt{Addition: Addition{Password: "default", RemotePathEncrypted: true}}
	encrypted, password := d.encryptionForDir("/plain")
	if !encrypted || password != "default" {
		t.Fatalf("expected default encryption, got encrypted=%v password=%q", encrypted, password)
	}
}

func TestEncryptionForDirUsesRulesWhenRemotePathEncryptionDisabled(t *testing.T) {
	d := &FastWebdavCrypt{Addition: Addition{
		Password:            "default",
		RemotePathEncrypted: false,
		EncryptedDirs:       "/secret\n/video/private=custom",
	}}
	d.encryptedDirs = d.parseEncryptedDirs()

	cases := []struct {
		path      string
		encrypted bool
		password  string
	}{
		{path: "/", encrypted: false, password: "default"},
		{path: "/normal", encrypted: false, password: "default"},
		{path: "/secret", encrypted: true, password: "default"},
		{path: "/secret/sub", encrypted: true, password: "default"},
		{path: "/video/private", encrypted: true, password: "custom"},
		{path: "/video/private/sub", encrypted: true, password: "custom"},
		{path: "/video/private-other", encrypted: false, password: "default"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			encrypted, password := d.encryptionForDir(tc.path)
			if encrypted != tc.encrypted || password != tc.password {
				t.Fatalf("got encrypted=%v password=%q, want encrypted=%v password=%q", encrypted, password, tc.encrypted, tc.password)
			}
		})
	}
}

func TestEncryptionForDirLongestRuleWins(t *testing.T) {
	d := &FastWebdavCrypt{Addition: Addition{
		Password:            "default",
		RemotePathEncrypted: true,
		EncryptedDirs:       "/secret=one\n/secret/sub=two",
	}}
	d.encryptedDirs = d.parseEncryptedDirs()

	encrypted, password := d.encryptionForDir("/secret/sub/filedir")
	if !encrypted || password != "two" {
		t.Fatalf("expected longest rule password two, got encrypted=%v password=%q", encrypted, password)
	}
}
