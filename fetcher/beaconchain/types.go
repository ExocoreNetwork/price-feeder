package beaconchain

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cometbft/cometbft/libs/sync"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"gopkg.in/yaml.v2"
)

type source struct {
	logger feedertypes.LoggerInf
	*types.Source
}
type config struct {
	URL   string `yaml:"url"`
	NSTID string `yaml:"nstid"`
}

type ResultConfig struct {
	Data struct {
		SlotsPerEpoch string `json:"SLOTS_PER_EPOCH"`
	} `json:"data"`
}

type stakerVList struct {
	locker      *sync.RWMutex
	sValidators map[int]*validatorList
}

func newStakerVList() *stakerVList {
	return &stakerVList{
		locker:      new(sync.RWMutex),
		sValidators: make(map[int]*validatorList),
	}
}

func (s *stakerVList) length() int {
	if s == nil {
		return 0
	}
	s.locker.RLock()
	l := len(s.sValidators)
	s.locker.RUnlock()
	return l
}

func (s *stakerVList) getStakerValidators() map[int]*validatorList {
	s.locker.RLock()
	defer s.locker.RUnlock()
	ret := make(map[int]*validatorList)
	for stakerIdx, vList := range s.sValidators {
		validators := make([]string, len(vList.validators))
		copy(validators, vList.validators)
		ret[stakerIdx] = &validatorList{
			index:      vList.index,
			validators: validators,
		}
	}
	return ret
}

func (s *stakerVList) addVIdx(sIdx int, vIdx string, index uint64) bool {
	s.locker.Lock()
	defer s.locker.Unlock()
	if vList, ok := s.sValidators[sIdx]; ok {
		if vList.index+1 != index {
			return false
		}
		vList.index++
		vList.validators = append(vList.validators, vIdx)
	} else {
		if index != 0 {
			return false
		}
		s.sValidators[sIdx] = &validatorList{
			index:      0,
			validators: []string{vIdx},
		}
	}
	return true
}
func (s *stakerVList) removeVIdx(sIdx int, vIdx string, index uint64) bool {
	s.locker.RLock()
	defer s.locker.RUnlock()
	if vList, ok := s.sValidators[sIdx]; ok {
		if vList.index+1 != index {
			return false
		}
		vList.index++
		for idx, v := range vList.validators {
			if v == vIdx {
				if len(vList.validators) == 1 {
					delete(s.sValidators, sIdx)
					return true
				}
				vList.validators = append(vList.validators[:idx], vList.validators[idx+1:]...)
				return true
			}
		}
	}
	return false
}

func (s *stakerVList) reset(stakerInfos []*oracletypes.StakerInfo, all bool) error {
	s.locker.Lock()
	if all {
		s.sValidators = make(map[int]*validatorList)
	}
	for _, stakerInfo := range stakerInfos {
		validators := make([]string, 0, len(stakerInfo.ValidatorPubkeyList))
		for _, validatorIndexHex := range stakerInfo.ValidatorPubkeyList {
			validatorIdx, err := convertHexToIntStr(validatorIndexHex)
			if err != nil {
				logger.Error("failed to convert validatorIndex from hex string to int", "validator-index-hex", validatorIndexHex)
				return fmt.Errorf(fmt.Sprintf("failed to convert validatorIndex from hex string to int, validator-index-hex:%s", validatorIndexHex))
			}
			validators = append(validators, validatorIdx)
		}

		index := uint64(0)
		// TODO: this may not necessary, stakerInfo should have at least one entry in balanceList
		if l := len(stakerInfo.BalanceList); l > 0 {
			index = stakerInfo.BalanceList[l-1].Index
		}
		s.sValidators[int(stakerInfo.StakerIndex)] = &validatorList{
			index:      index,
			validators: validators,
		}

	}
	s.locker.Unlock()
	return nil
}

const (
	envConf               = "oracle_env_beaconchain.yaml"
	urlQuerySlotsPerEpoch = "eth/v1/config/spec"
	hexPrefix             = "0x"
)

var (
	logger        feedertypes.LoggerInf
	defaultSource *source
)

func init() {
	types.SourceInitializers[types.BeaconChain] = initBeaconchain
}

func initBeaconchain(cfgPath string, l feedertypes.LoggerInf) (types.SourceInf, error) {
	if logger = l; logger == nil {
		if logger = feedertypes.GetLogger("fetcher_beaconchain"); logger == nil {
			return nil, feedertypes.ErrInitFail.Wrap("logger is not initialized")
		}
	}
	// init from config file
	cfg, err := parseConfig(cfgPath)
	if err != nil {
		// logger.Error("fail to parse config", "error", err, "path", cfgPath)
		return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse config, error:%v", err))
	}
	// beaconchain endpoint url
	urlEndpoint, err = url.Parse(cfg.URL)
	if err != nil {
		return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse url:%s, error:%v", cfg.URL, err))
	}

	// parse nstID by splitting it with "_'"
	nstID := strings.Split(cfg.NSTID, "_")
	if len(nstID) != 2 {
		return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("invalid nstID format, nstID:%s", nstID))
	}
	// the second element is the lzID of the chain, trim possible prefix_0x
	lzID, err := strconv.ParseUint(strings.TrimPrefix(nstID[1], hexPrefix), 16, 64)
	if err != nil {
		return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse lzID:%s from nstID, error:%v", nstID[1], err))
	}

	// set slotsPerEpoch
	if slotsPerEpochKnown, ok := types.ChainToSlotsPerEpoch[lzID]; ok {
		slotsPerEpoch = slotsPerEpochKnown
	} else {
		// else, we need the slotsPerEpoch from beaconchain endpoint
		u := urlEndpoint.JoinPath(urlQuerySlotsPerEpoch)
		res, err := http.Get(u.String())
		if err != nil {
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to get slotsPerEpoch from endpoint:%s, error:%v", u.String(), err))
		}
		result, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to get slotsPerEpoch from endpoint:%s, error:%v", u.String(), err))
		}
		var re ResultConfig
		if err = json.Unmarshal(result, &re); err != nil {
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse response from slotsPerEpoch, error:%v", err))
		}
		if slotsPerEpoch, err = strconv.ParseUint(re.Data.SlotsPerEpoch, 10, 64); err != nil {
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse response_slotsPerEoch, got:%s, rror:%v", re.Data.SlotsPerEpoch, err))
		}
	}

	// init first to get a fixed point for 'fetch' to refer to
	defaultSource = &source{}
	*defaultSource = source{
		logger: logger,
		Source: types.NewSource(logger, types.BeaconChain, defaultSource.fetch, cfgPath, defaultSource.reload),
	}

	// initialize native-restaking stakers' beaconchain-validator list

	// update nst assetID to be consistent with exocored. for beaconchain it's about different lzID
	types.SetNativeAssetID(fetchertypes.NativeTokenETH, cfg.NSTID)

	return defaultSource, nil
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

func convertHexToIntStr(hexStr string) (string, error) {
	vBytes, err := hexutil.Decode(hexStr)
	if err != nil {
		return "", err
	}
	return new(big.Int).SetBytes(vBytes).String(), nil

}
