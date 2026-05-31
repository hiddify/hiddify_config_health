package core

func init() {
	Register("hiddify-core", func(binPath string) Core {
		if binPath == "" {
			binPath = binFromEnv("HIDDIFY_BIN")
		}
		return &processCore{
			name:    "hiddify-core",
			binPath: binPath,
			// hiddify-core CLI: `hiddify-core run <config>`
			runArgs:   func(cfg string) []string { return []string{"run", cfg} },
			checkArgs: nil, // no check subcommand yet
		}
	})
}
