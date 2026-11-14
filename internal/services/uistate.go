package services

import "lontar/internal/appstate"

// UIStateService keeps small UI values for the frontend (panel layouts,
// column widths, panel toggles) in state.json, so they survive restarts.
// Values are opaque strings (the frontend stores JSON).
type UIStateService struct {
	store *appstate.Store
}

// NewUIStateService returns a UIStateService.
func NewUIStateService(st *appstate.Store) *UIStateService { return &UIStateService{store: st} }

// All returns every stored value (empty when state.json is missing or
// unreadable).
func (s *UIStateService) All() map[string]string { return s.store.UI() }

// Set stores value under key; an empty value removes it.
func (s *UIStateService) Set(key, value string) error { return s.store.SetUI(key, value) }
