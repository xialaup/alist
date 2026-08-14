package fastwebdav_crypt

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	RemotePath          string `json:"remote_path" required:"true" help:"Path where files are stored"`
	RemotePathEncrypted bool   `json:"remote_path_encrypted" type:"bool" default:"true" help:"Decrypt the whole remote path by default"`
	// encrypted_dirs is only needed when remote_path_encrypted is disabled or when a subdirectory needs a different password.
	// Use one rule per line, relative to this mount: /dir uses the default password, /dir=password overrides it.
	EncryptedDirs string `json:"encrypted_dirs" type:"text" help:"One rule per line, relative to this mount: /dir uses the default password, /dir=password overrides it. Longest path wins."`
	Password      string `json:"password" required:"true" confidential:"true" help:"Default FastWebdav encryption password"`
	ShowHidden    bool   `json:"show_hidden" default:"true"`
}

var config = driver.Config{
	Name:        "FastWebdav Crypt",
	LocalSort:   true,
	OnlyProxy:   true,
	NoCache:     true,
	NoUpload:    true,
	DefaultRoot: "/",
	Alert:       "warning|Read-only driver for playing FastWebdav encrypted files from another storage.",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &FastWebdavCrypt{}
	})
}
