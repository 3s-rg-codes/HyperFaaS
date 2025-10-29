package utils

import "strings"

type StringList []string

func (a *StringList) String() string {
	return strings.Join(*a, ",")
}

func (a *StringList) Set(value string) error {
	*a = append(*a, value)
	return nil
}
