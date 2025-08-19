package keyValueStore

import (
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type FunctionMetadataStore interface {
	Put(image *common.Image, config *common.Config) (id string, err error)
	Get(functionID string) (imageTag string, config *common.Config, err error)
}
