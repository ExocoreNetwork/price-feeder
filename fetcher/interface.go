package fetcher

type F interface {
	// start fetching all tokens' prices
	StartAll()
	// stop fetching all tokens' prices
	StopAll()
	// start fetching a specific token's price
	Start()
	// stop fetching a specific token's price
	Stop()
	// add a token into token set for price fetching
	AddToken()
	// remove a token from the token set
	RemoveToken()
	// TODO: add a specific source, could be user defined as a rpc server, this fetcher worked as a client to request price from that server // force add will replace existed living source if any
	//	RegisterSource()
	// TODO: remove a specific source
	//	RemoveSource()
	// config fetching interval(currently support the same interval for all tokens)
	SetInterval()
	// config source with supported tokens
	ConfigSource(source string, tokens ...string)

	// GetAllprices retreive all alive token's prices from fetcher
	GetAllLatestPrices()
	//
	GetLatestPricesAllSources(token string)
	//
	GetLatestPrice(srouce, token string)
	// TODO: support history price ? not neccessary
}

/*
this feeder tool only serves as offical usage
AddToken is called when params udpated some tokens
AddSource is a cmd command
*/
