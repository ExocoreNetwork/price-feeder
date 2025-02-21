package beaconchain

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/imroc/biu"
	oracletypes "github.com/imua-xyz/imuachain/x/oracle/types"
	"github.com/imua-xyz/price-feeder/fetcher/types"
	feedertypes "github.com/imua-xyz/price-feeder/types"
)

type ResultValidators struct {
	Data []struct {
		Index     string `json:"index"`
		Validator struct {
			Pubkey           string `json:"pubkey"`
			EffectiveBalance string `json:"effective_balance"`
		} `json:"validator"`
	} `json:"data"`
}

type ResultHeader struct {
	Data struct {
		Header struct {
			Message struct {
				Slot      string `json:"slot"`
				StateRoot string `json:"state_root"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

type ValidatorPostRequest struct {
	IDs []string `json:"ids"`
}

const (
	defaultBalance = 32
	divisor        = 1000000000
	maxChange      = -32

	urlQueryHeader          = "eth/v1/beacon/headers"
	urlQueryHeaderFinalized = "eth/v1/beacon/headers/finalized"

	getValidatorsPath = "eth/v1/beacon/states/%s/validators"
)

var (
	// updated from oracle, deposit/withdraw
	// TEST only. debug
	//	validatorsTmp = []string{
	//		"0xa1d1ad0714035353258038e964ae9675dc0252ee22cea896825c01458e1807bfad2f9969338798548d9858a571f7425c",
	//		"0xb2ff4716ed345b05dd1dfc6a5a9fa70856d8c75dcc9e881dd2f766d5f891326f0d10e96f3a444ce6c912b69c22c6754d",
	//	}
	//
	//	stakerValidators = map[int]*validatorList{2: {0, validatorsTmp}}
	// stakerValidators  = make(map[int]*validatorList)
	defaultStakerValidators = newStakerVList()

	// latest finalized epoch we've got balances summarized for stakers
	finalizedEpoch   uint64
	finalizedVersion int64

	// latest stakerBalanceChanges, initialized as 0 change (256-0 of 1st parts means that all stakers have 32 efb)
	//	latestChangesBytes = make([]byte, 32)
	latestChangesBytes = types.NSTETHZeroChanges

	urlEndpoint   *url.URL
	slotsPerEpoch uint64
)

func ResetStakerValidators(stakerInfos []*oracletypes.StakerInfo, version int64, all bool) error {
	return defaultStakerValidators.reset(stakerInfos, version, all)
}

func UpdateStakerValidators(add, remove map[int64][]string, startVersion, endVersion int64) error {
	return defaultStakerValidators.update(add, remove, startVersion, endVersion)
}

func (s *source) fetch(token string) (*types.PriceInfo, error) {
	// check epoch, when epoch updated, update effective-balance
	if types.NSTToken(token) != types.NativeTokenETH {
		return nil, feedertypes.ErrTokenNotSupported.Wrap(fmt.Sprintf("only support native-eth-restaking %s, got:%s", types.NativeTokenETH, token))
	}

	stakerValidators, version := defaultStakerValidators.getStakerValidators()
	if len(stakerValidators) == 0 {
		// return zero price when there's no stakers
		return &types.PriceInfo{}, nil
	}

	// check if finalized epoch had been updated
	epoch, stateRoot, err := getFinalizedEpoch()
	if err != nil {
		return nil, fmt.Errorf("fail to get finalized epoch from beaconchain, error:%w", err)
	}

	// epoch not updated, just return without fetching since effective-balance has not changed
	if epoch <= finalizedEpoch && version <= finalizedVersion {
		return &types.PriceInfo{
			Price: string(latestChangesBytes),
			// combine epoch and version as roundID in priceInfo
			RoundID: fmt.Sprintf("%s_%s", strconv.FormatUint(finalizedEpoch, 10), strconv.FormatInt(version, 10)),
		}, nil
	}

	stakerChanges := make([][]int, 0, len(stakerValidators))
	s.logger.Info("fetch efb from beaconchain", "stakerList_length", len(stakerValidators))
	hasEFBChanged := false
	for stakerIdx, validators := range stakerValidators {
		stakerBalance := 0
		// beaconcha.in support at most 100 validators for one request
		l := len(validators)
		i := 0
		for l > 100 {
			tmpValidatorPubkeys := validators[i : i+100]
			i += 100
			l -= 100
			validatorBalances, err := getValidators(tmpValidatorPubkeys, stateRoot)
			if err != nil {
				return nil, fmt.Errorf("failed to get validators from beaconchain, error:%w", err)
			}
			for _, validatorBalance := range validatorBalances {
				stakerBalance += int(validatorBalance[1])
			}
		}

		validatorBalances, err := getValidators(validators[i:], stateRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to get validators from beaconchain, error:%w", err)
		}
		for _, validatorBalance := range validatorBalances {
			// this should be initialized from imuad
			stakerBalance += int(validatorBalance[1])
		}
		if delta := stakerBalance - defaultBalance*l; delta != 0 {
			if delta < maxChange*l {
				delta = maxChange * l
			}
			// #nosec G155  -- safe conversion, stakerIdx has been verified to be less than 256
			stakerChanges = append(stakerChanges, []int{int(stakerIdx), delta})
			s.logger.Info("fetched efb from beaconchain", "staker_index", stakerIdx, "balance_change", delta, "validators_count", l)
			hasEFBChanged = true
		}
	}
	if !hasEFBChanged && len(stakerValidators) > 0 {
		s.logger.Info("fetch efb from beaconchain, all efbs of validators remains to 32 without any change")
	}
	sort.Slice(stakerChanges, func(i, j int) bool {
		return stakerChanges[i][0] < stakerChanges[j][0]
	})

	finalizedEpoch = epoch
	finalizedVersion = version

	latestChangesBytes = convertBalanceChangeToBytes(stakerChanges)

	return &types.PriceInfo{
		Price:   string(latestChangesBytes),
		RoundID: fmt.Sprintf("%s_%s", strconv.FormatUint(finalizedEpoch, 10), strconv.FormatInt(version, 10)),
	}, nil
}

// reload does nothing since beaconchain source only used to update the balance change for nsteth
func (s *source) reload(token, cfgPath string) error {
	return nil
}

func convertBalanceChangeToBytes(stakerChanges [][]int) []byte {
	if len(stakerChanges) == 0 {
		// lenght equals to 0 means that alls takers have efb of 32 with 0 changes
		ret := make([]byte, 32)
		return ret
	}
	str := ""
	index := 0
	changeBytesList := make([][]byte, 0, len(stakerChanges))
	bitsList := make([]int, 0, len(stakerChanges))
	for _, stakerChange := range stakerChanges {
		str += strings.Repeat("0", stakerChange[0]-index) + "1"
		index = stakerChange[0] + 1

		// change amount -> bytes
		change := stakerChange[1]
		var changeBytes []byte
		symbol := 1
		if change < 0 {
			symbol = -1
			change *= -1
		}
		change--
		bits := 0
		if change == 0 {
			bits = 1
			changeBytes = []byte{byte(0)}
		} else {
			tmpChange := change
			for tmpChange > 0 {
				bits++
				tmpChange /= 2
			}
			if change < 256 {
				// 1 byte
				changeBytes = []byte{byte(change)}
				changeBytes[0] <<= (8 - bits)
			} else {
				// 2 byte
				changeBytes = make([]byte, 2)
				binary.BigEndian.PutUint16(changeBytes, uint16(change))
				moveLength := 16 - bits
				changeBytes[0] <<= moveLength
				tmp := changeBytes[1] >> (8 - moveLength)
				changeBytes[0] |= tmp
				changeBytes[1] <<= moveLength
			}
		}

		// use lower 4 bits to represent the length of valid change value in bits format
		bitsLengthBytes := []byte{byte(bits)}
		bitsLengthBytes[0] <<= 4
		if symbol < 0 {
			bitsLengthBytes[0] |= 8
		}

		tmp := changeBytes[0] >> 5
		bitsLengthBytes[0] |= tmp
		if bits <= 3 {
			changeBytes = nil
		} else {
			changeBytes[0] <<= 3
		}

		if len(changeBytes) == 2 {
			tmp = changeBytes[1] >> 5
			changeBytes[0] |= tmp
			if bits <= 11 {
				changeBytes = changeBytes[:1]
			} else {
				changeBytes[1] <<= 3
			}
		}
		bitsLengthBytes = append(bitsLengthBytes, changeBytes...)
		changeBytesList = append(changeBytesList, bitsLengthBytes)
		bitsList = append(bitsList, bits)
	}

	l := len(bitsList)
	changeResult := changeBytesList[l-1]
	bitsList[len(bitsList)-1] = bitsList[len(bitsList)-1] + 5
	for i := l - 2; i >= 0; i-- {
		prev := changeBytesList[i]

		byteLength := 8 * len(prev)
		bitsLength := bitsList[i] + 5
		// delta must <8
		delta := byteLength - bitsLength
		if delta == 0 {
			changeResult = append(prev, changeResult...)
			bitsList[i] = bitsLength + bitsList[i+1]
		} else {
			// delta : (0,8)
			tmp := changeResult[0] >> (8 - delta)
			prev[len(prev)-1] |= tmp
			if len(changeResult) > 1 {
				for j := 1; j < len(changeResult); j++ {
					changeResult[j-1] <<= delta
					tmp := changeResult[j] >> (8 - delta)
					changeResult[j-1] |= tmp
				}
			}
			changeResult[len(changeResult)-1] <<= delta
			left := bitsList[i+1] % 8
			if bitsList[i+1] > 0 && left == 0 {
				left = 8
			}
			if left <= delta {
				changeResult = changeResult[:len(changeResult)-1]
			}
			changeResult = append(prev, changeResult...)
			bitsList[i] = bitsLength + bitsList[i+1]
		}
	}
	str += strings.Repeat("0", 256-index)
	bytesIndex := biu.BinaryStringToBytes(str)

	result := append(bytesIndex, changeResult...)
	return result
}

func getValidators(validators []string, stateRoot string) ([][]uint64, error) {
	reqBody := ValidatorPostRequest{
		IDs: validators,
	}
	body, _ := json.Marshal(reqBody)
	u := urlEndpoint.JoinPath(fmt.Sprintf(getValidatorsPath, stateRoot))
	res, err := http.Post(u.String(), "application/json", bytes.NewBuffer(body))
	if err != nil {
		logger.Error("failed to get validators from beaconchain", "error", err)
		return nil, err
	}
	defer res.Body.Close()
	result, _ := io.ReadAll(res.Body)
	re := ResultValidators{}
	if err := json.Unmarshal(result, &re); err != nil {
		logger.Error("failed to parse GetValidators response", "error", err)
		return nil, err
	}
	ret := make([][]uint64, 0, len(re.Data))
	for _, value := range re.Data {
		index, _ := strconv.ParseUint(value.Index, 10, 64)
		efb, _ := strconv.ParseUint(value.Validator.EffectiveBalance, 10, 64)
		ret = append(ret, []uint64{index, efb / divisor})
	}
	return ret, nil
}

func getFinalizedEpoch() (epoch uint64, stateRoot string, err error) {
	u := urlEndpoint.JoinPath(urlQueryHeaderFinalized)
	var res *http.Response
	res, err = http.Get(u.String())
	if err != nil {
		return
	}
	result, _ := io.ReadAll(res.Body)
	defer res.Body.Close()
	re := ResultHeader{}
	if err = json.Unmarshal(result, &re); err != nil {
		return
	}
	slot, _ := strconv.ParseUint(re.Data.Header.Message.Slot, 10, 64)
	epoch = slot / slotsPerEpoch
	if slot%slotsPerEpoch > 0 {
		u = urlEndpoint.JoinPath(urlQueryHeader, strconv.FormatUint(epoch*slotsPerEpoch, 10))
		res, err = http.Get(u.String())
		if err != nil {
			return
		}
		result, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if err = json.Unmarshal(result, &re); err != nil {
			return
		}
	}
	stateRoot = re.Data.Header.Message.StateRoot
	return
}
