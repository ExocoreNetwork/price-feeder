package types

import (
	"errors"
	"fmt"
	"os"

	"github.com/cometbft/cometbft/libs/log"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TODO: define the interface of fetchertypes.PriceInfo, for fetcher, imuaclient to referenced
// PriceInfoInf defines the core structure which has the value/data that is fetched from out-imua-chain source
// and submit to imua-chain
// type PriceInfoInf interface {
// 	SetPrice()
// 	SetDecimal()
// 	SetRoundID()
// 	SetTimeStamp()
//
// 	GetPrice()
// 	GetDecimal()
// 	GetRoundID()
// 	GetTimeStamp()
// }

type PrivValidatorKey struct {
	Address string `json:"address"`
	PrivKey struct {
		Value string `json:"value"`
	} `json:"priv_key"`
}

type TokenSources struct {
	Token   string `mapstructure:"token"`
	Sources string `mapstructure:"sources"`
}

type Config struct {
	Tokens []TokenSources `mapstructure:"tokens"`
	Sender struct {
		Mnemonic string `mapstructure:"mnemonic"`
		Path     string `mapstructure:"path"`
	} `mapstructure:"sender"`
	Imua struct {
		ChainID string `mapstructure:"chainid"`
		AppName string `mapstructure:"appname"`
		Grpc    string `mapstructure:"grpc"`
		Ws      string `mapstructure:"ws"`
		Rpc     string `mapstructure:"rpc"`
	} `mapstructure:"imua"`
	Debugger struct {
		Grpc string `mapstructure:"grpc"`
	} `mapstructure:"debugger"`
}

type LoggerInf log.Logger

const TimeLayout = "2006-01-02 15:04:05"

var logger log.Logger = NewLogger(zapcore.InfoLevel)

type LoggerWrapper struct {
	*zap.SugaredLogger
}

func (l *LoggerWrapper) Info(msg string, keyvals ...interface{}) {
	l.Infow(msg, keyvals...)
}
func (l *LoggerWrapper) Debug(msg string, keyvals ...interface{}) {
	l.Debugw(msg, keyvals...)
}
func (l *LoggerWrapper) Error(msg string, keyvals ...interface{}) {
	l.Errorw(msg, keyvals...)
}

func (l *LoggerWrapper) With(keyvals ...interface{}) log.Logger {
	return &LoggerWrapper{
		l.SugaredLogger.With(keyvals...),
	}
}

func NewLogger(level zapcore.Level) *LoggerWrapper {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	config.Encoding = "console"
	config.Level = zap.NewAtomicLevelAt(level)
	config.EncoderConfig.StacktraceKey = ""
	logger, _ := config.Build()
	return &LoggerWrapper{
		logger.Sugar(),
	}
}

func SetLogger(l LoggerInf) LoggerInf {
	if l != nil {
		logger = l
	}
	return logger
}

func GetLogger(component string) LoggerInf {
	if logger == nil {
		return nil
	}
	if len(component) > 0 {
		return logger.With("component", component)
	}
	return logger
}

type Err struct {
	parent  *Err
	message string
}

func NewErr(message string) *Err {
	return &Err{
		parent:  nil,
		message: message,
	}
}

func (e *Err) Error() string {
	details := e.message
	m := e.Unwrap()
	if mErr, ok := m.(*Err); ok {
		for mErr != nil {
			details = fmt.Sprintf("%s.{%s}", mErr.message, details)
			e = mErr
			m = e.Unwrap()
			if mErr, ok = m.(*Err); !ok {
				break
			}
		}
	}
	return fmt.Sprintf("err:%s, details:{%s}", e.message, details)
}

func (e *Err) Wrap(message string) *Err {
	return &Err{
		parent:  e,
		message: message,
	}
}

func (e *Err) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.parent
}

var (
	v *viper.Viper

	ErrInitFail                 = NewErr("failed to initialization")
	ErrInitConnectionFail       = NewErr("failed to establish a connection")
	ErrSourceTokenNotConfigured = NewErr("token not configured")
	ErrTokenNotSupported        = NewErr("token not supported")
)

// InitConfig will only read path cfgFile once, and for reload after InitConfig, should use ReloadConfig
func InitConfig(cfgFile string) (*Config, error) {
	if len(cfgFile) == 0 {
		return nil, errors.New("empty file name")
	}
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		return nil, err
	}
	if v == nil {
		v = viper.New()
	}
	v.SetConfigFile(cfgFile)
	v.SetConfigType("yaml")
	// If a config file is found, read it in.
	if err := v.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", v.ConfigFileUsed())
	}

	conf := &Config{}
	if err := v.Unmarshal(conf); err != nil {
		panic(ErrInitFail.Wrap(err.Error()))
	}
	return conf, nil
}

// ReloadConfig will reload config file with path set by InitConfig
func ReloadConfig() Config {

	// If a config file is found, read it in.
	if err := v.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", v.ConfigFileUsed())
	}

	conf := &Config{}
	if err := v.Unmarshal(conf); err != nil {
		fmt.Fprintln(os.Stderr, "parse config file failed:", v.ConfigFileUsed())
	}
	return *conf
}
