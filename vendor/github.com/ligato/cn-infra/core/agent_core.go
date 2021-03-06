// Copyright (c) 2017 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/ligato/cn-infra/logging"
	"github.com/ligato/cn-infra/utils/safeclose"
	"github.com/namsral/flag"
)

// variables set by the Makefile using ldflags
var (
	BuildVersion string
	BuildDate    string
)

// Agent implements startup & shutdown procedures.
type Agent struct {
	// plugin list
	plugins []*NamedPlugin
	logging.Logger
	// agent startup details
	startup
}

type startup struct {
	// The startup/initialization must take no longer that maxStartup.
	MaxStartupTime time.Duration
	// successfully initialized plugins
	initDuration time.Duration
	// successfully after-initialized plugins
	afterInitDuration time.Duration
	// the field is set before initialization of every plugin with its name
	currentlyProcessing string
}

const (
	logErrorFmt        = "plugin %s: Init error '%s', took %v"
	logSuccessFmt      = "plugin %s: Init took %v"
	logSkippedFmt      = "plugin %s: Init skipped due to previous error"
	logAfterSkippedFmt = "plugin %s: AfterInit skipped due to previous error"
	logAfterErrorFmt   = "plugin %s: AfterInit error '%s', took %v"
	logAfterSuccessFmt = "plugin %s: AfterInit took %v"
	logNoAfterInitFmt  = "plugin %s: not implement AfterInit"
	logTimeoutFmt      = "plugin %s not completed before timeout"
	// The default value serves as an indicator for timer still running even after MaxStartupTime. Used in case
	// some plugin lasts long time to load or is stuck
	defaultTimerValue = -1
)

// NewAgent returns a new instance of the Agent with plugins.
// <logger> will be used to log messages related to the agent life-cycle,
// but not for the plugins themselves.
// <maxStartup> puts a time limit on initialization of all provided plugins.
// Agent.Start() returns ErrPluginsInitTimeout error if one or more plugins fail
// to initialize inside the specified time limit.
// <plugins> is a variable list of plugins to load. ListPluginsInFlavor() helper
// method can be used to obtain the list from a given flavor.
func NewAgent(logger logging.Logger, maxStartup time.Duration, plugins ...*NamedPlugin) *Agent {
	a := Agent{
		plugins,
		logger,
		startup{MaxStartupTime: maxStartup},
	}
	return &a
}

// Start starts/initializes all selected plugins.
// The first iteration tries to run Init() method on every plugin from the list.
// If any of the plugins fails to initialize (Init() return non-nil error),
// initialization is cancelled by calling Close() method for already initialized
// plugins in the reverse order. The encountered error is returned by this
// function as-is.
// The second iteration does the same for the AfterInit() method. The difference
// is that AfterInit() is an optional method (not required by the Plugin
// interface, only suggested by PostInit interface) and therefore not necessarily
// called on every plugin.
// The startup/initialization must take no longer than maxStartup time limit,
// otherwise ErrPluginsInitTimeout error is returned.
func (agent *Agent) Start() error {
	agent.WithFields(logging.Fields{"BuildVersion": BuildVersion, "BuildDate": BuildDate}).Info("Starting the agent...")

	doneChannel := make(chan struct{}, 0)
	errChannel := make(chan error, 0)

	if !flag.Parsed() {
		flag.Parse()
	}

	go func() {
		err := agent.initPlugins()
		if err != nil {
			errChannel <- err
			return
		}
		err = agent.handleAfterInit()
		if err != nil {
			errChannel <- err
			return
		}
		close(doneChannel)
	}()

	//block until all Plugins are initialized or timeout expires
	select {
	case err := <-errChannel:
		agent.WithField("durationInNs", agent.initDuration.Nanoseconds()).Infof("Agent Init took %v", agent.initDuration)
		agent.WithField("durationInNs", agent.afterInitDuration.Nanoseconds()).Infof("Agent AfterInit took %v", agent.afterInitDuration)
		return err
	case <-doneChannel:
		agent.WithField("durationInNs", agent.initDuration.Nanoseconds()).Infof("Agent Init took %v", agent.initDuration)
		agent.WithField("durationInNs", agent.afterInitDuration.Nanoseconds()).Infof("Agent AfterInit took %v", agent.afterInitDuration)
		duration := agent.initDuration + agent.afterInitDuration
		agent.WithField("durationInNs", duration.Nanoseconds()).Info(fmt.Sprintf("All plugins initialized successfully, took %v", duration))
		return nil
	case <-time.After(agent.MaxStartupTime):
		if agent.initDuration == defaultTimerValue {
			agent.Infof("Agent Init took > %v", agent.MaxStartupTime)
			agent.WithField("durationInNs", agent.afterInitDuration.Nanoseconds()).Infof("Agent AfterInit took %v", agent.afterInitDuration)
		} else if agent.afterInitDuration == defaultTimerValue {
			agent.WithField("durationInNs", agent.initDuration.Nanoseconds()).Infof("Agent Init took %v", agent.initDuration)
			agent.Infof("Agent AfterInit took > %v", agent.MaxStartupTime)
		}

		return fmt.Errorf(logTimeoutFmt, agent.currentlyProcessing)
	}
}

// Stop gracefully shuts down the Agent. It is called usually when the user
// interrupts the Agent from the EventLoopWithInterrupt().
//
// This implementation tries to call Close() method on every plugin on the list
// in the reverse order. It continues even if some error occurred.
func (agent *Agent) Stop() error {
	agent.Info("Stopping agent...")
	errMsg := ""
	for i := len(agent.plugins) - 1; i >= 0; i-- {
		agent.WithField("pluginName", agent.plugins[i].PluginName).Debug("Stopping plugin begin")
		err := safeclose.Close(agent.plugins[i].Plugin)
		if err != nil {
			if len(errMsg) > 0 {
				errMsg += "; "
			}
			errMsg += string(agent.plugins[i].PluginName)
			errMsg += ": " + err.Error()
		}
		agent.WithField("pluginName", agent.plugins[i].PluginName).Debug("Stopping plugin end ", err)
	}

	agent.Debug("Agent stopped")

	if len(errMsg) > 0 {
		return errors.New(errMsg)
	}
	return nil
}

// initPlugins calls Init() an all plugins on the list
func (agent *Agent) initPlugins() error {
	// Flag indicates that some of the plugins failed to initialize
	var initPluginCounter int
	var pluginFailed bool
	var wasError error

	agent.initDuration = defaultTimerValue
	initStartTime := time.Now()
	for index, plugin := range agent.plugins {
		initPluginCounter = index

		// set currently initialized plugin name
		agent.currentlyProcessing = string(plugin.PluginName)

		// skip all other plugins if some of them failed
		if pluginFailed {
			agent.Info(fmt.Sprintf(logSkippedFmt, plugin.PluginName))
			continue
		}

		pluginStartTime := time.Now()
		err := plugin.Init()
		if err != nil {
			pluginErrTime := time.Since(pluginStartTime)
			agent.WithField("durationInNs", pluginErrTime.Nanoseconds()).Errorf(logErrorFmt, plugin.PluginName, err, pluginErrTime)

			pluginFailed = true
			wasError = fmt.Errorf(logErrorFmt, plugin.PluginName, err, pluginErrTime)
		} else {
			pluginSuccTime := time.Since(pluginStartTime)
			agent.WithField("durationInNs", pluginSuccTime.Nanoseconds()).Infof(logSuccessFmt, plugin.PluginName, pluginSuccTime)
		}
	}
	agent.initDuration = time.Since(initStartTime)

	if wasError != nil {
		//Stop the plugins that are initialized
		for i := initPluginCounter; i >= 0; i-- {
			agent.Debugf("Closing %v", agent.plugins[i])
			err := safeclose.Close(agent.plugins[i])
			if err != nil {
				wasError = err
			}
		}
		return wasError
	}
	return nil
}

// handleAfterInit calls the AfterInit handlers for plugins that can only
// finish their initialization after  all other plugins have been initialized.
func (agent *Agent) handleAfterInit() error {
	// Flag indicates that some of the plugins failed to after-initialize
	var pluginFailed bool
	var wasError error

	agent.afterInitDuration = defaultTimerValue
	afterInitStartTime := time.Now()
	for _, plug := range agent.plugins {
		// set currently after-initialized plugin name
		agent.currentlyProcessing = string(plug.PluginName)

		// skip all other plugins if some of them failed
		if pluginFailed {
			agent.Info(fmt.Sprintf(logAfterSkippedFmt, plug.PluginName))
			continue
		}

		// Check if plugin implements AfterInit()
		if plugin, ok := plug.Plugin.(PostInit); ok {
			pluginStartTime := time.Now()
			err := plugin.AfterInit()
			if err != nil {
				pluginErrTime := time.Since(pluginStartTime)
				agent.WithField("durationInNs", pluginErrTime.Nanoseconds()).Errorf(logAfterErrorFmt, plug.PluginName, err, pluginErrTime)

				pluginFailed = true
				wasError = fmt.Errorf(logAfterErrorFmt, plug.PluginName, err, pluginErrTime)
			} else {
				pluginSuccTime := time.Since(pluginStartTime)
				agent.WithField("durationInNs", pluginSuccTime.Nanoseconds()).Infof(logAfterSuccessFmt, plug.PluginName, pluginSuccTime)
			}
		} else {
			agent.Info(fmt.Sprintf(logNoAfterInitFmt, plug.PluginName))
		}
	}
	agent.afterInitDuration = time.Since(afterInitStartTime)

	if wasError != nil {
		agent.Stop()
		return wasError
	}
	return nil
}