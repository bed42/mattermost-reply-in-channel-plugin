package main

import (
	"reflect"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration.
type configuration struct {
	// BlockEchoReplies controls the echo-thread guard: rejecting the first
	// reply typed into a /ric channel post's thread, and refusing /ric from
	// inside such threads. Matches the "BlockEchoReplies" settings_schema
	// key. It is a *bool because a fresh install has no plugin-settings
	// entry at all: nil must mean ENABLED (the schema default), not Go's
	// zero value false.
	BlockEchoReplies *bool
}

// blockEchoReplies resolves the tri-state configuration: unset means enabled.
func (c *configuration) blockEchoReplies() bool {
	return c.BlockEchoReplies == nil || *c.BlockEchoReplies
}

// Clone shallow copies the configuration.
func (c *configuration) Clone() *configuration {
	var clone = *c
	return &clone
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return err
	}

	p.setConfiguration(configuration)

	return nil
}
