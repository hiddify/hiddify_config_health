package runner

// RunConfig is the schema of run.json placed next to each example's config files.
type RunConfig struct {
	// Name is a human-readable label shown in the web UI and CLI output.
	Name string `json:"name"`

	// Core is "sing-box", "xray", or "hiddify-core".
	Core string `json:"core"`

	// ServerConfig and ClientConfig are paths relative to the run.json directory.
	// When Topology is non-empty these are ignored.
	ServerConfig string `json:"server_config"`
	ClientConfig string `json:"client_config"`

	// Topology describes a multi-hop chain. When set it replaces ServerConfig/ClientConfig.
	Topology []TopologyNode `json:"topology,omitempty"`

	// Vars are substitution values. "auto" is resolved at runtime.
	Vars map[string]string `json:"vars"`

	// Deploy configures SSH deployment of the server process.
	Deploy DeployConfig `json:"deploy,omitempty"`

	// TLS, when true, auto-generates a self-signed cert bundle and injects
	// {{TLS_CERT}}, {{TLS_KEY}}, {{TLS_CA}}, {{CA_FINGERPRINT}} placeholders.
	TLS bool `json:"tls,omitempty"`

	// Checks lists which health checks to run.
	// Valid values: dns, tcp-dns, http, quic, ping, download, upload, speedtest.
	Checks []string `json:"checks"`

	// BeforeStart are shell commands run before starting any process.
	BeforeStart []string `json:"before_start,omitempty"`
	// AfterStop are shell commands run after all processes are stopped.
	AfterStop []string `json:"after_stop,omitempty"`

	// TimeoutSec is the per-check timeout in seconds (default 30).
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// TopologyNode describes one node in a multi-hop chain.
type TopologyNode struct {
	// Role is "server", "relay", or "client".
	Role string `json:"role"`
	// Core overrides the top-level Core for this node.
	Core   string `json:"core,omitempty"`
	Config string `json:"config"`
	// Host is an optional SSH URL for remote nodes (ssh://user@host:port).
	Host string `json:"host,omitempty"`
}

// DeployConfig controls SSH deployment of the server process.
type DeployConfig struct {
	// URL is ssh://user@host:port. Empty = local.
	URL string `json:"url,omitempty"`
	// RemoteDir is the working directory on the remote host (default /tmp/hch).
	RemoteDir string `json:"remote_dir,omitempty"`
}

func (d DeployConfig) IsRemote() bool { return d.URL != "" }
