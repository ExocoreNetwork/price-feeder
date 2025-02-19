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
	version     int64
	sValidators sVList
}

type sVList map[int64][]string

func newSVList() sVList {
	return make(map[int64][]string)
}

func (sv sVList) cpy() sVList {
	ret := make(map[int64][]string)
	for k, v := range sv {
		sliceCpy := make([]string, len(v))
		copy(sliceCpy, v)
		ret[k] = sliceCpy
	}
	return ret
}

func (sv sVList) add(sValidators map[int64][]string) error {
	for sIdx, validators := range sValidators {
		if vList, ok := sv[sIdx]; ok {
			seen := make(map[string]struct{})
			for _, vIdx := range vList {
				seen[vIdx] = struct{}{}
			}
			for _, vIdx := range validators {
				if _, ok := seen[vIdx]; ok {
					return fmt.Errorf("failed to do add, validatorIndex already exists, staker-index:%d, validator-index:%s", sIdx, vIdx)
				}
				sv[sIdx] = append(sv[sIdx], vIdx)
				seen[vIdx] = struct{}{}
			}
		} else {
			sv[sIdx] = validators
		}
	}
	return nil
}

func (sv sVList) remove(sValidators map[int64][]string) error {
	for sIdx, validators := range sValidators {
		vList, ok := sv[sIdx]
		if !ok {
			return fmt.Errorf("failed to do remove, stakerIndex not found, staker-index:%d", sIdx)
		}
		target := make(map[string]struct{})
		for _, v := range validators {
			target[v] = struct{}{}
		}
		result := make([]string, 0, len(vList))
		for _, v := range vList {
			if _, ok := target[v]; !ok {
				result = append(result, v)
			}
		}
		if len(vList)-len(result) != len(validators) {
			return fmt.Errorf("failed to remove validators, including non-existing validatorIndex to remove, staker-index:%d", sIdx)
		}
		sv[sIdx] = result
	}
	return nil
}

func newStakerVList() *stakerVList {
	return &stakerVList{
		locker:      new(sync.RWMutex),
		sValidators: newSVList(),
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

// func (s *stakerVList) getStakerValidators() map[int]*validatorList {
func (s *stakerVList) getStakerValidators() (map[int64][]string, int64) {
	s.locker.RLock()
	defer s.locker.RUnlock()
	ret := s.sValidators.cpy()
	version := s.version
	return ret, version
}

func (s *stakerVList) add(sValidators map[int64][]string, nextVersion, latestVersion int64) bool {
	s.locker.Lock()
	defer s.locker.Unlock()
	if s.version+1 != nextVersion {
		return false
	}
	cpy := s.sValidators.cpy()
	if cpy.add(sValidators) == nil {
		s.sValidators = cpy
		s.version = latestVersion
		return true
	}
	return false
}

func (s *stakerVList) remove(sVList map[int64][]string, nextVersion, latestVersion int64) bool {
	s.locker.RLock()
	defer s.locker.RUnlock()
	if s.version+1 != nextVersion {
		return false
	}
	cpy := s.sValidators.cpy()
	if cpy.remove(sVList) == nil {
		s.sValidators = cpy
		s.version = latestVersion
		return true
	}
	return false
}

func (s *stakerVList) update(addList, removeList map[int64][]string, nextVersion, latestVersion int64) error {
	s.locker.Lock()
	defer s.locker.Unlock()
	if s.version+1 != nextVersion {
		return fmt.Errorf("version mismatch, current:%d, next:%d", s.version, nextVersion)
	}
	cpy := s.sValidators.cpy()
	if err := cpy.add(addList); err != nil {
		return err
	}
	if err := cpy.remove(removeList); err != nil {
		return err
	}

	s.sValidators = cpy
	s.version = latestVersion
	return nil
}

func (s *stakerVList) reset(stakerInfos []*oracletypes.StakerInfo, version int64, all bool) error {
	s.locker.Lock()
	defer s.locker.Unlock()

	if !all && s.version+1 != version {
		return fmt.Errorf("version mismatch, current:%d, next:%d", s.version, version)
	}
	tmp := make(map[int64][]string)
	seenStakerIdx := make(map[int64]struct{})
	for _, stakerInfo := range stakerInfos {
		if _, ok := seenStakerIdx[stakerInfo.StakerIndex]; ok {
			return fmt.Errorf(fmt.Sprintf("duplicated stakerIndex, staker-index:%d", stakerInfo.StakerIndex))
		}
		// TODO: V1 support only 256 stakers at most for now(each stakers can have at most 256 beaconchain validators), grouops stakers as list of 256-length for more stakers
		if stakerInfo.StakerIndex > 256 {
			return fmt.Errorf(fmt.Sprintf("stakerIndex is greater than 256, staker-index:%d", stakerInfo.StakerIndex))
		}
		validators := make([]string, 0, len(stakerInfo.ValidatorPubkeyList))
		seenValidatorIdx := make(map[string]struct{})
		for _, validatorIndexHex := range stakerInfo.ValidatorPubkeyList {
			if _, ok := seenValidatorIdx[validatorIndexHex]; ok {
				return fmt.Errorf(fmt.Sprintf("duplicated validatorIndex, validator-index-hex:%s", validatorIndexHex))
			}
			validatorIdx, err := convertHexToIntStr(validatorIndexHex)
			if err != nil {
				return fmt.Errorf(fmt.Sprintf("failed to convert validatorIndex from hex string to int, validator-index-hex:%s", validatorIndexHex))
			}
			validators = append(validators, validatorIdx)
			seenValidatorIdx[validatorIndexHex] = struct{}{}
		}
		seenStakerIdx[stakerInfo.StakerIndex] = struct{}{}
		tmp[stakerInfo.StakerIndex] = validators
	}
	// all or zero
	if all {
		s.sValidators = tmp
	} else {
		for k, v := range tmp {
			s.sValidators[k] = v
		}
	}

	s.version = version
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

func ConvertHexToIntStrForMap(in map[int64][]string) (map[int64][]string, error) {
	ret := make(map[int64][]string)
	var err error
	for k, v := range in {
		ret[k] = make([]string, len(v))
		for i, val := range v {
			ret[k][i], err = convertHexToIntStr(val)
			if err != nil {
				return nil, err
			}
		}
	}
	return ret, nil
}
