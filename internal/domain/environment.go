package domain

import "time"

type Environment struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Channel   string            `json:"channel"`
	Roots     map[string]string `json:"roots"`
	Resolved  map[string]string `json:"resolved"`
	Revision  uint64            `json:"revision"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type EnvironmentInput struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Channel string            `json:"channel"`
	Roots   map[string]string `json:"roots"`
}

type ResolutionInput struct {
	Channel string            `json:"channel"`
	Roots   map[string]string `json:"roots"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

type Resolution struct {
	CatalogRevision uint64            `json:"catalog_revision"`
	Channel         string            `json:"channel"`
	ChannelFallback bool              `json:"channel_fallback"`
	Resolved        map[string]string `json:"resolved"`
	Channels        map[string]string `json:"channels"`
	Edges           []Edge            `json:"edges"`
	Steps           int               `json:"steps"`
}
