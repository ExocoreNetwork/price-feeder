package beaconchain

import (
	"errors"
	"fmt"
	"testing"
)

type stakerList struct {
	stakerAddrs []string
}

func TestDecode(t *testing.T) {
	bytes := convertBalanceChangeToBytes([][]int{
		{1, -20},
		{2, 10},
	})
	stakerChanges, err := parseBalanceChange(bytes, stakerList{
		stakerAddrs: []string{
			"staker_0",
			"staker_1",
			"staker_2",
		},
	})

	fmt.Println(stakerChanges, err)
}

func parseBalanceChange(rawData []byte, sl stakerList) (map[string]int, error) {
	indexs := rawData[:32]
	changes := rawData[32:]
	index := -1
	byteIndex := 0
	bitOffset := 0
	lengthBits := 5
	stakerChanges := make(map[string]int)
	for _, b := range indexs {
		for i := 7; i >= 0; i-- {
			index++
			if (b>>i)&1 == 1 {
				lenValue := changes[byteIndex] << bitOffset
				bitsLeft := 8 - bitOffset
				lenValue >>= (8 - lengthBits)
				if bitsLeft < lengthBits {
					byteIndex++
					lenValue |= changes[byteIndex] >> (8 - lengthBits + bitsLeft)
					bitOffset = lengthBits - bitsLeft
				} else {
					if bitOffset += lengthBits; bitOffset == 8 {
						bitOffset = 0
					}
					if bitsLeft == lengthBits {
						byteIndex++
					}
				}

				symbol := lenValue & 1
				lenValue >>= 1
				if lenValue <= 0 {
					return stakerChanges, errors.New("length of change value must be at least 1 bit")
				}

				bitsExtracted := 0
				stakerChange := 0
				for bitsExtracted < int(lenValue) {
					bitsLeft := 8 - bitOffset
					byteValue := changes[byteIndex] << bitOffset
					if (int(lenValue) - bitsExtracted) < bitsLeft {
						bitsLeft = int(lenValue) - bitsExtracted
						bitOffset += bitsLeft
					} else {
						byteIndex++
						bitOffset = 0
					}
					byteValue >>= (8 - bitsLeft)
					stakerChange = (stakerChange << bitsLeft) | int(byteValue)
					bitsExtracted += bitsLeft
				}
				stakerChange++
				if symbol == 1 {
					stakerChange *= -1
				}
				stakerChanges[sl.stakerAddrs[index]] = stakerChange
			}
		}
	}
	return stakerChanges, nil
}
