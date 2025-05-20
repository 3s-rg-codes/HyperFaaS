package keyValueStore

import (
	"fmt"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type NoSuchKeyError struct {
	Key *common.FunctionID
}

func (e NoSuchKeyError) Error() string {
	return fmt.Sprintf("No such key, %v", e.Key)
}
