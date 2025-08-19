package keyValueStore

import (
	"fmt"
)

type NoSuchKeyError struct {
	Key string
}

func (e NoSuchKeyError) Error() string {
	return fmt.Sprintf("No such key, %v", e.Key)
}
