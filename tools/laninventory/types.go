package laninventory

type ipAddress struct {
	Address string `json:"address"`
}

type adapter struct {
	Name         string      `json:"name"`
	MACAddress   string      `json:"macAddress"`
	IPv4         []ipAddress `json:"ipv4"`
	PrefixLength int         `json:"prefixLength"`
	Gateways     []string    `json:"gateways"`
}

type networkState struct {
	Primary  adapter   `json:"primary"`
	Adapters []adapter `json:"adapters"`
}
