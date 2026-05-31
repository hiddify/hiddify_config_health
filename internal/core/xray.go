package core

func init() {
	Register("xray", func(binPath string) Core {
		if binPath == "" {
			binPath = binFromEnv("XRAY_BIN")
		}
		return &processCore{
			name:      "xray",
			binPath:   binPath,
			runArgs:   func(cfg string) []string { return []string{"run", "-c", cfg} },
			checkArgs: []string{"run", "-test", "-c"},
		}
	})
}
