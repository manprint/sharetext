package main

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type webAppManifest struct {
	ID              string         `json:"id,omitempty"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description,omitempty"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	Orientation     string         `json:"orientation,omitempty"`
	ThemeColor      string         `json:"theme_color,omitempty"`
	BackgroundColor string         `json:"background_color,omitempty"`
	Lang            string         `json:"lang,omitempty"`
	Icons           []manifestIcon `json:"icons,omitempty"`
}

func defaultManifestIcons() []manifestIcon {
	return []manifestIcon{
		{Src: "/static/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
		{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
		{Src: "/static/icon-maskable.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "maskable"},
		{Src: "/static/favicon.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "any"},
	}
}

func defaultManifest() webAppManifest {
	return webAppManifest{
		Name:            "sharetext",
		ShortName:       "sharetext",
		Description:     "Condivisione di snippet di testo in tempo reale.",
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		Orientation:     "any",
		ThemeColor:      "#0f766e",
		BackgroundColor: "#fafaf9",
		Lang:            "it",
		Icons:           defaultManifestIcons(),
	}
}

func sessionLaunchPath(slug string) string {
	return "/launch/" + slug
}

func sessionManifestPath(slug string) string {
	return "/manifest/session/" + slug + ".webmanifest"
}

func manifestShortName(slug string) string {
	if len(slug) <= 12 {
		return slug
	}
	return slug[:12]
}

func sessionManifest(slug string) webAppManifest {
	m := defaultManifest()
	launchPath := sessionLaunchPath(slug)
	m.ID = launchPath
	m.Name = "sharetext · " + slug
	m.ShortName = manifestShortName(slug)
	m.Description = "Apri direttamente la sessione " + slug + "."
	m.StartURL = launchPath
	return m
}
