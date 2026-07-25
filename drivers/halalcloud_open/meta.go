package halalcloud_open

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	driver.RootPath
	RefreshToken string `json:"refresh_token" required:"false" help:"If using a personal API approach, the RefreshToken is not required."`
	UploadThread int    `json:"upload_thread" type:"number" default:"3" help:"1 <= thread <= 32"`

	ClientID     string `json:"client_id" required:"true" default:""`
	ClientSecret string `json:"client_secret" required:"true" default:""`
	Host         string `json:"host" required:"false" default:"openapi.2dland.cn"`
	TimeOut      int    `json:"timeout" type:"number" default:"60" help:"timeout in seconds"`
}

var config = driver.Config{
	Name:        "HalalCloudOpen",
	OnlyProxy:   false,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &HalalCloudOpen{}
	})
}

type UploadedFile struct {
	Identity        string `json:"identity"`
	UserIdentity    string `json:"user_identity"`
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	ContentIdentity string `json:"content_identity"`
}
