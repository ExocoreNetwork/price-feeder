package beaconchain

import (
	"net/url"
	"testing"
)

func TestGetEpoch(t *testing.T) {
	//	GetValidators([]string{"1", "5", "9"}, 0)
	validators := []string{
		"0xa1d1ad0714035353258038e964ae9675dc0252ee22cea896825c01458e1807bfad2f9969338798548d9858a571f7425c",
		"0xb2ff4716ed345b05dd1dfc6a5a9fa70856d8c75dcc9e881dd2f766d5f891326f0d10e96f3a444ce6c912b69c22c6754d",
	}
	urlEndpoint, _ = url.Parse("https://rpc.ankr.com/premium-http/eth_beacon/a5d2626c4027e6d924c870d2558bc774bc995ce917a17a654a92856b3279a586")
	_, stateRoot, _ := GetFinalizedEpoch()
	//GetValidatorsAnkr([]string{"0xa1d1ad0714035353258038e964ae9675dc0252ee22cea896825c01458e1807bfad2f9969338798548d9858a571f7425c"}, stateRoot)
	//GetValidatorsAnkr([]string{"0xb2ff4716ed345b05dd1dfc6a5a9fa70856d8c75dcc9e881dd2f766d5f891326f0d10e96f3a444ce6c912b69c22c6754d"}, stateRoot)
	GetValidators(validators, stateRoot)
}
