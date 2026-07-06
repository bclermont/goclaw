package cmd

import (
	"github.com/nextlevelbuilder/goclaw/internal/audio"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/webcall"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// registerWebCallFactory wires the webcall channel factory into the
// InstanceLoader. Called from the gateway setup after all stores are initialised.
func registerWebCallFactory(loader *channels.InstanceLoader, audioMgr *audio.Manager, sessStore store.SessionStore) {
	loader.RegisterFactory(channels.TypeWebCall, webcall.FactoryWithAudio(audioMgr, sessStore))
}
