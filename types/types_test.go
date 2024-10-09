package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrWrap(t *testing.T) {
	e := NewErr("parent")
	e1 := e.Wrap("second")
	e2 := e1.Wrap("third")
	assert.Equal(t, errors.Is(e2, e), true)

	fmt.Println(e2.Error())
	e0 := NewErr("parent")
	assert.Equal(t, errors.Is(e0, e), false)
}
