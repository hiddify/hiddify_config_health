package core

func init() {
	Register("sing-box", func(binPath string) Core {
		if binPath == "" {
			binPath = binFromEnv("SINGBOX_BIN")
		}
		return &processCore{
			name:      "sing-box",
			binPath:   binPath,
			runArgs:   func(cfg string) []string { return []string{"run", "-c", cfg} },
			checkArgs: []string{"check", "-c"},
		}
	})
}
