package main

import (
	"reflect"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration.
type configuration struct {
	// BlockEchoReplies is the explicit admin override for the echo-thread
	// guard, as a tri-state dropdown ("" / "true" / "false") so the UI can
	// express "use the install default" instead of forcing a choice.
	// "" means unset: the per-install default resolved at activation applies
	// (fresh installs block, upgrades of installs with /ric history do not).
	BlockEchoReplies string
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
