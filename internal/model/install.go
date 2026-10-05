package model

// InstallFile is install.json (design-spec.md, install.json), its fields in
// the schema's order.
type InstallFile struct {
	Schema      InstallVersion   `json:"schema"`
	HookBinary  string           `json:"hook_binary"`
	Version     string           `json:"version"`
	InstalledAt Timestamp        `json:"installed_at"`
	Locations   InstallLocations `json:"locations"`
}

// InstallLocations are the locations install resolved: absolute paths.
type InstallLocations struct {
	ConfigDir      string `json:"config_dir"`
	StateDir       string `json:"state_dir"`
	ClaudeSettings string `json:"claude_settings"`
}

// ReadInstall reads install.json. Its content is meaningful only when the
// result is usable.
func ReadInstall(data []byte) (InstallFile, FileResult) {
	return readFile(data, InstallSchema, func(in *InstallFile, f *Fields, p *Problems) {
		in.HookBinary = absolute(f, p, "hook_binary")
		if v, ok := f.Required("version"); ok {
			in.Version, _ = p.String(v, f.Ptr("version"))
		}
		in.InstalledAt = timestamp(f, p, "installed_at")
		if v, ok := f.Required("locations"); ok {
			if lf, ok := p.Object(v, f.Ptr("locations")); ok {
				in.Locations.ConfigDir = absolute(lf, p, "config_dir")
				in.Locations.StateDir = absolute(lf, p, "state_dir")
				in.Locations.ClaudeSettings = absolute(lf, p, "claude_settings")
				lf.Done()
			}
		}
	})
}

func absolute(f *Fields, p *Problems, key string) string {
	if v, ok := f.Required(key); ok {
		s, _ := p.absolute(v, f.Ptr(key))
		return s
	}
	return ""
}
