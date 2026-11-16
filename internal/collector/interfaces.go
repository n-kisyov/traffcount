package collector

// AdapterMeta holds enriched network adapter properties across platforms.
type AdapterMeta struct {
	Name        string
	Description string
	MAC         string
	IPv4        string
	IPv6        string
	Gateway     string
	LinkSpeed   uint64 // bits per second
	IsLoopback  bool
	IsUp        bool
}
