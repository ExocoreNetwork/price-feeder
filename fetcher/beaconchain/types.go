package beaconchain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cometbft/cometbft/libs/sync"
	"gopkg.in/yaml.v2"
)

//	type stakerList struct {
//		StakerAddrs []string
//	}
//
//	type validatorList struct {
//		index      uint64
//		validators []string
//	}

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

const (
	envConf               = "oracle_env_beaconchain.yaml"
	urlQuerySlotsPerEpoch = "eth/v1/config/spec"
	hexPrefix             = "0x"
)

var (
	logger        feedertypes.LoggerInf
	lock          sync.RWMutex
	defaultSource *source

	// errors
	errTokenNotSupported = errors.New("token not supported")
)

func init() {
	types.SourceInitializers[types.BeaconChain] = initBeaconchain
}

func initBeaconchain(cfgPath string) (types.SourceInf, error) {
	// init logger, panic immediately if logger has not been set properly
	if logger = feedertypes.GetLogger("fetcher_beaconchain"); logger == nil {
		panic("logger is not initialized")
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

	defaultSource := &source{
		logger: logger,
		Source: types.NewSource(logger, types.BeaconChain, defaultSource.fetch, cfgPath, defaultSource.reload),
	}

	// initialize native-restaking stakers' beaconchain-validator list

	// update nst assetID to be consistent with exocored. for beaconchain it's about different lzID
	types.UpdateNativeAssetID(cfg.NSTID)
	//	types.Fetchers[types.BeaconChain] = Fetch

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
