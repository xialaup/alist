package _123Share

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	ShareKey string `json:"sharekey" required:"true"`
	SharePwd string `json:"sharepassword"`
	driver.RootID
	//OrderBy        string `json:"order_by" type:"select" options:"file_name,size,update_at" default:"file_name"`
	//OrderDirection string `json:"order_direction" type:"select" options:"asc,desc" default:"asc"`
	AccessToken string `json:"accesstoken" type:"text"`
	UserAgent   string `json:"user_agent"  required:"false" default:"Mozilla/5.0 (iPad; CPU OS 11_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/11.0 Tablet/15E148 Safari/604.1"`
}

var config = driver.Config{
	Name:              "123PanShare",
	LocalSort:         true,
	OnlyLocal:         false,
	OnlyProxy:         false,
	NoCache:           false,
	NoUpload:          true,
	NeedMs:            false,
	DefaultRoot:       "0",
	CheckStatus:       false,
	Alert:             "",
	NoOverwriteUpload: false,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Pan123Share{}
	})
}
