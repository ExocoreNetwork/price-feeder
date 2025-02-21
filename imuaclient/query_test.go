package imuaclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuery(t *testing.T) {
	cc, _, _ := CreateGrpcConn("127.0.0.1:9090")
	defer cc.Close()
	p, err := GetParams(cc)
	assert.NoError(t, err)
	assert.Equal(t, "ETH", p.Tokens[1].Name)
}
