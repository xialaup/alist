package chunk

import "github.com/alist-org/alist/v3/internal/model"

type chunkObject struct {
	model.Object
	chunkSizes []int64
}
