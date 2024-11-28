package beaconchain

import (
	"fmt"
	"testing"
)

func TestDecode(t *testing.T) {
	bytes := convertBalanceChangeToBytes([][]int{
		{1, -20},
		{2, 10},
	})
	stakerChanges, err := parseBalanceChange(bytes, stakerList{
		StakerAddrs: []string{
			"staker_0",
			"staker_1",
			"staker_2",
		},
	})

	fmt.Println(stakerChanges, err)
}
