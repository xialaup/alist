package chunk

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	RemotePath string `json:"remote_path" required:"true"`
	PartSize   int64  `json:"part_size" required:"true" type:"number" help:"bytes"`
	CustomExt  string `json:"custom_ext" type:"string"`
	StoreHash  bool   `json:"store_hash" type:"bool" default:"true"`

	Thumbnail  bool `json:"thumbnail" required:"true" default:"false" help:"enable thumbnail which pre-generated under .thumbnails folder"`
	ShowHidden bool `json:"show_hidden"  default:"true" required:"false" help:"show hidden directories and files"`
}

var config = driver.Config{
	Name:        "Chunk",
	LocalSort:   true,
	OnlyProxy:   true,
	OnlyLocal:   true,
	NoCache:     true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Chunk{}
	})
}
