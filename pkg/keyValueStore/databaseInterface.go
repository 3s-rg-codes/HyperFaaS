package keyValueStore

import (
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type FunctionMetadataStore interface {
	Put(tag *common.ImageTag, config *common.Config) (id *common.FunctionID, err error)
	Get(functionID *common.FunctionID) (imageTag *common.ImageTag, config *common.Config, err error)
}
