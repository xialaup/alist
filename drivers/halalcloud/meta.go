package halalcloud

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	// Usually one of two
	driver.RootPath
	// define other
	RefreshToken   string `json:"refresh_token" required:"true" help:"login type is refresh_token,this is required"`
	UploadThread   string `json:"upload_thread" default:"3" help:"1 <= thread <= 32"`
	UseDavMode     bool   `json:"use_webdav" default:"false" help:""`
	WebDavUserName string `json:"webdav_username" default:"" help:"auto fetch"`
	WebDavPassWord string `json:"webdav_password" default:"" help:"auto fetch"`

	AppID      string `json:"app_id" required:"true" default:"alist/10001"`
	AppVersion string `json:"app_version" required:"true" default:"1.0.0"`
	AppSecret  string `json:"app_secret" required:"true" default:"bR4SJwOkvnG5WvVJ"`
}

var DefaultProxy = true
var DeafultWebDavPolicy = "native_proxy"

var config = driver.Config{
	Name:                "HalalCloud",
	LocalSort:           false,
	OnlyLocal:           false,
	OnlyProxy:           false,
	NoCache:             false,
	NoUpload:            false,
	NeedMs:              false,
	DefaultRoot:         "/",
	CheckStatus:         false,
	Alert:               "",
	NoOverwriteUpload:   false,
	DeafultProxy:        &DefaultProxy,
	DeafultWebDavPolicy: &DeafultWebDavPolicy,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &HalalCloud{}
	})
}
