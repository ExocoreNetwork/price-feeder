package types

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
)

func TestConfig(t *testing.T) {
	conf := InitConfig("./config-bak.yaml")
	tmp := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, conf.Tokens[2].Sources)
	fmt.Println(strings.Split(tmp, ","))
}
