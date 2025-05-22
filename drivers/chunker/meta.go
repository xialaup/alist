package chunker

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	RemotePath string `json:"remote_path" required:"true" help:"This is where the encrypted data stores"`
	ChunkSize  int64  `json:"chunk_size" type:"number" default:"50" help:"chunk size while uploading (unit: MB)"`
}

var config = driver.Config{
	Name:              "Chunker",
	LocalSort:         true,
	OnlyLocal:         false,
	OnlyProxy:         true,
	NoCache:           true,
	NoUpload:          false,
	NeedMs:            false,
	DefaultRoot:       "/",
	CheckStatus:       false,
	Alert:             "",
	NoOverwriteUpload: false,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Chunker{}
	})
}
