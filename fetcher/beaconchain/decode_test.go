package beaconchain

import (
	"fmt"
	"testing"
)

func TestDecode(t *testing.T) {
	bytes := convertBalanceChangeToBytes([][]int{})
	stakerChanges, err := parseBalanceChange(bytes, stakerList{})

	fmt.Println(stakerChanges, err)
}
