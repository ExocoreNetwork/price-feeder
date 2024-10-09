package beaconchain

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cometbft/cometbft/libs/sync"
	"github.com/imroc/biu"
	"gopkg.in/yaml.v2"
)

type stakerList struct {
	StakerAddrs []string
}

type validatorList struct {
	index      uint64
	validators []string
}

type DataValidatorBalance []struct {
	ValidatorIndex   uint64 `json:"validatorindex"`
	EffectiveBalance uint64 `json:"effectivebalance"`
	Pubkey           string `json:"pubkey"`
}

type DataEpoch struct {
	Epoch uint64 `json:"epoch"`
}

type ResultEpoch struct {
	DataEpoch `json:"data"`
}

type ResultValidatorBalances struct {
	DataValidatorBalance `json:"data"`
}

type validator struct {
	Pubkey           string `json:"pubkey"`
	EffectiveBalance string `json:"effective_balance"`
}

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

type validatorPostRequest struct {
	IDs []string `json:"ids"`
}

type config struct {
	URL string `yaml:"url"`
}

const (
	defaultBalance = 32
	divisor        = 1000000000
	maxChange      = -16

	envConf = "oracle_env_beaconchain.yaml"
)

var (
	lock sync.RWMutex
	// updated from oracle, deposit/withdraw
	// TEST only. debug
	//	validatorsTmp = []string{
	//		"0xa1d1ad0714035353258038e964ae9675dc0252ee22cea896825c01458e1807bfad2f9969338798548d9858a571f7425c",
	//		"0xb2ff4716ed345b05dd1dfc6a5a9fa70856d8c75dcc9e881dd2f766d5f891326f0d10e96f3a444ce6c912b69c22c6754d",
	//	}
	//
	//	stakerValidators = map[int]*validatorList{2: {0, validatorsTmp}}
	stakerValidators = make(map[int]*validatorList)
	// latest finalized epoch we've got balances summarized for stakers
	finalizedEpoch uint64
	// latest stakerBalanceChanges, initialized as 0 change (256-0 of 1st parts means that all stakers have 32 efb)
	latestChangesBytes = make([]byte, 32)

	// errors
	errTokenNotSupported = errors.New("token not supported")
	// urlQueryValidatorBalances, _ = url.Parse("https://beaconcha.in/api/v1/validator")
	// queryValue                   = url.Values(map[string][]string{"offset": {"0"}, "limit": {"1"}})

	urlQueryHeader          = "eth/v1/beacon/headers"
	urlQueryHeaderFinalized = "eth/v1/beacon/headers/finalized"

	getValidatorsPath = "eth/v1/beacon/states/%s/validators"
	urlEndpoint       *url.URL
)

func init() {
	types.InitFetchers[types.BeaconChain] = Init
}

func parseConfig(confPath string) (config, error) {
	yamlFile, err := os.Open(path.Join(confPath, envConf))
	if err != nil {
		return config{}, err
	}
	cfg := config{}
	if err = yaml.NewDecoder(yamlFile).Decode(&cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func Init(confPath string) error {
	cfg, err := parseConfig(confPath)
	if err != nil {
		// panic if config init error
		panic(feedertypes.ErrInitFail.Wrap(err.Error()))
	}
	urlEndpoint, err = url.Parse(cfg.URL)
	if err != nil {
		// panic if config init error
		panic(feedertypes.ErrInitFail.Wrap(err.Error()))
	}
	types.Fetchers[types.BeaconChain] = Fetch
	return nil
}

func ResetStakerValidators(stakerInfos []*oracletypes.StakerInfo) {
	lock.Lock()
	for _, sInfo := range stakerInfos {
		//		stakerValidators[int(sInfo.StakerIndex)] = sInfo.ValidatorPubkeyList
		index := uint64(0)
		if l := len(sInfo.BalanceList); l > 0 {
			index = sInfo.BalanceList[l-1].Index
		}
		stakerValidators[int(sInfo.StakerIndex)] = &validatorList{
			index:      index,
			validators: sInfo.ValidatorPubkeyList,
		}
	}
	lock.Unlock()
}

// UpdateStakerValidators update staker's validators for deposit/withdraw events triggered by exocore
func UpdateStakerValidators(stakerIdx int, validatorPubkey string, deposit bool, index uint64) bool {
	lock.Lock()
	defer lock.Unlock()
	// add a new valdiator for the staker
	if deposit {
		if vList, ok := stakerValidators[stakerIdx]; ok {
			if vList.index+1 != index {
				return false
			}
			vList.index++
			vList.validators = append(vList.validators, validatorPubkey)
		} else {
			stakerValidators[stakerIdx] = &validatorList{
				index:      index,
				validators: []string{validatorPubkey},
			}
		}
	} else {
		// remove the existing validatorIndex for the corresponding staker
		if vList, ok := stakerValidators[stakerIdx]; ok {
			if vList.index+1 != index {
				return false
			}
			vList.index++
			for idx, v := range vList.validators {
				if v == validatorPubkey {
					if len(vList.validators) == 1 {
						delete(stakerValidators, stakerIdx)
						break
					}
					vList.validators = append(vList.validators[:idx], vList.validators[idx+1:]...)
					break
				}
			}
		}
	}
	return true
}

func ResetStakerValidatorsForAll(stakerInfos []*oracletypes.StakerInfo) {
	lock.Lock()
	stakerValidators = make(map[int]*validatorList)
	for _, stakerInfo := range stakerInfos {
		validators := make([]string, 0, len(stakerInfo.ValidatorPubkeyList))

		for _, validatorPubkey := range stakerInfo.ValidatorPubkeyList {
			validators = append(validators, validatorPubkey)
		}

		index := uint64(0)
		// TODO: this may not necessary, stakerInfo should have at least one entry in balanceList
		if l := len(stakerInfo.BalanceList); l > 0 {
			index = stakerInfo.BalanceList[l-1].Index
		}
		stakerValidators[int(stakerInfo.StakerIndex)] = &validatorList{
			index:      index,
			validators: stakerInfo.ValidatorPubkeyList,
		}
	}
	lock.Unlock()
}

func Fetch(token string) (*types.PriceInfo, error) {
	// check epoch, when epoch updated, update effective-balance
	if token != types.NativeTokenETH {
		log.Printf("fetch_beaconchain. only support native-eth-restaking, expect:%s, got:%s", types.NativeTokenETH, token)
		return nil, errTokenNotSupported
	}
	// check if finalized epoch had been updated
	epoch, stateRoot, err := GetFinalizedEpoch()
	if err != nil {
		log.Println("fetch_beaconchain. GetFinalizedEpoch fail", err)
		return nil, err
	}

	// epoch not updated, just return without fetching since effective-balance has not changed
	if epoch <= finalizedEpoch {
		return &types.PriceInfo{
			Price:   string(latestChangesBytes),
			RoundID: strconv.FormatUint(finalizedEpoch, 10),
		}, nil
	}

	stakerChanges := make([][]int, 0, len(stakerValidators))

	lock.RLock()
	for stakerIdx, vList := range stakerValidators {
		stakerBalance := 0
		// beaconcha.in support at most 100 validators for one request
		l := len(vList.validators)
		i := 0
		for l > 100 {
			tmpValidatorPubkeys := vList.validators[i : i+100]
			i += 100
			l -= 100
			// validatorBalances, err := GetValidators(tmpValidatorIdx, epoch)
			validatorBalances, err := GetValidators(tmpValidatorPubkeys, stateRoot)
			if err != nil {
				log.Println("fetch_beaconchain. GetValidators fail", err)
				return nil, err
			}
			for _, validatorBalance := range validatorBalances {
				stakerBalance += int(validatorBalance[1])
			}
		}

		// validatorBalances, err := GetValidators(validatorIdxs, epoch)
		validatorBalances, err := GetValidators(vList.validators[i:], stateRoot)
		if err != nil {
			log.Println("fetch_beaconchain. GetValidators fail", err.Error())
			return nil, err
		}
		for _, validatorBalance := range validatorBalances {
			// this should be initialized from exocored
			stakerBalance += int(validatorBalance[1])
		}
		if delta := stakerBalance - defaultBalance*len(vList.validators); delta != 0 {
			if delta < maxChange {
				delta = maxChange
			}
			stakerChanges = append(stakerChanges, []int{stakerIdx, delta})
		}
	}
	lock.RUnlock()

	finalizedEpoch = epoch

	latestChangesBytes = convertBalanceChangeToBytes(stakerChanges)

	return &types.PriceInfo{
		Price:   string(latestChangesBytes),
		RoundID: strconv.FormatUint(finalizedEpoch, 10),
	}, nil
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
				stakerChanges[sl.StakerAddrs[index]] = stakerChange
			}
		}
	}
	return stakerChanges, nil
}

func GetValidators(validators []string, stateRoot string) ([][]uint64, error) {
	reqBody := validatorPostRequest{
		IDs: validators,
	}
	body, _ := json.Marshal(reqBody)
	u := urlEndpoint.JoinPath(fmt.Sprintf(getValidatorsPath, stateRoot))
	res, err := http.Post(u.String(), "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Println("GetValidators fail", err)
		return nil, err
	}
	defer res.Body.Close()
	result, _ := io.ReadAll(res.Body)
	re := ResultValidators{}
	if err := json.Unmarshal(result, &re); err != nil {
		log.Println("GetValidators fail", err)
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

func GetFinalizedEpoch() (epoch uint64, stateRoot string, err error) {
	u := urlEndpoint.JoinPath(urlQueryHeaderFinalized)
	var res *http.Response
	res, err = http.Get(u.String())
	if err != nil {
		return
	}
	result, _ := io.ReadAll(res.Body)
	re := ResultHeader{}
	if err = json.Unmarshal(result, &re); err != nil {
		return
	}
	res.Body.Close()
	slot, _ := strconv.ParseUint(re.Data.Header.Message.Slot, 10, 64)
	epoch = slot / 32
	if slot%32 > 0 {
		u = urlEndpoint.JoinPath(urlQueryHeader, strconv.FormatUint(epoch*32, 10))
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
