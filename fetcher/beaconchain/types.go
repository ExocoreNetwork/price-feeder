package beaconchain

import (
	"encoding/json"
	"errors"
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
)

var (
	logger feedertypes.LoggerInf
	lock   sync.RWMutex

	// errors
	errTokenNotSupported = errors.New("token not supported")
)

func init() {
	types.InitFetchers[types.BeaconChain] = initBeaconchain
}

func initBeaconchain(confPath string) error {
	if logger = feedertypes.GetLogger("fetcher_beaconchain"); logger == nil {
		panic("logger is not initialized")
	}
	cfg, err := parseConfig(confPath)
	if err != nil {
		logger.Error("fail to parse config", "error", err, "path", confPath)
		return feedertypes.ErrInitFail.Wrap(err.Error())
	}
	urlEndpoint, err = url.Parse(cfg.URL)
	if err != nil {
		logger.Error("failed to parse beaconchain URL", "url", cfg.URL)
		return feedertypes.ErrInitFail.Wrap(err.Error())
	}

	// parse nstID by splitting it
	nstID := strings.Split(cfg.NSTID, "_")
	if len(nstID) != 2 {
		logger.Error("invalid nstID format, should be: x_y", "nstID", nstID)
		return feedertypes.ErrInitFail.Wrap("invalid nstID format")
	}
	// the second element is the lzID of the chain
	lzID, err := strconv.ParseUint(strings.TrimPrefix(nstID[1], "0x"), 16, 64)
	if err != nil {
		logger.Error("failed to pase lzID from nstID", "got_nstID", nstID[1], "error", err)
		return feedertypes.ErrInitFail.Wrap(err.Error())
	}

	// set slotsPerEpoch
	if slotsPerEpochKnown, ok := types.ChainToSlotsPerEpoch[lzID]; ok {
		slotsPerEpoch = slotsPerEpochKnown
	} else {
		// else, we need the slotsPerEpoch from beaconchain endpoint
		u := urlEndpoint.JoinPath(urlQuerySlotsPerEpoch)
		res, err := http.Get(u.String())
		if err != nil {
			logger.Error("failed to get slotsPerEpoch from endpoint", "error", err, "url", u.String())
			return feedertypes.ErrInitFail.Wrap(err.Error())
		}
		result, err := io.ReadAll(res.Body)
		if err != nil {
			logger.Error("failed to read response from slotsPerEpoch", "error", err)
			return feedertypes.ErrInitFail.Wrap(err.Error())
		}
		var re ResultConfig
		if err = json.Unmarshal(result, &re); err != nil {
			logger.Error("failed to parse response from slotsPerEpoch", "erro", err)
			return feedertypes.ErrInitFail.Wrap(err.Error())
		}
		if slotsPerEpoch, err = strconv.ParseUint(re.Data.SlotsPerEpoch, 10, 64); err != nil {
			logger.Error("failed to parse response_slotsPerEpoch", "got_res.data.slotsPerEpoch", re.Data.SlotsPerEpoch)
			return feedertypes.ErrInitFail.Wrap(err.Error())
		}
	}

	types.UpdateNativeAssetID(cfg.NSTID)
	types.Fetchers[types.BeaconChain] = Fetch

	return nil
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
