package pbreqres

import (
        "context"
        "sync"
)

type ConduitManager struct {
        conduits map[string]*Conduit
        mutex *sync.Mutex
}

type Conduit struct {
        channel chan PatchedRequest
        numRequesters int
        numResponders int
}

func NewConduitManager() *ConduitManager {
        conduits := make(map[string]*Conduit)
	mutex := &sync.Mutex{}
        return &ConduitManager{conduits: conduits, mutex: mutex}
}

func NewConduit() *Conduit {
        channel := make(chan PatchedRequest)
        return &Conduit{channel: channel}
}

func (m *ConduitManager) Request(channelId string, request PatchedRequest, ctx context.Context) bool {
        m.mutex.Lock()
        _, ok := m.conduits[channelId]
        if !ok {
                m.conduits[channelId] = NewConduit()
        }
        m.conduits[channelId].numRequesters += 1
        channel := m.conduits[channelId].channel
        m.mutex.Unlock()

        select {
        case channel <- request:
                m.mutex.Lock()
                m.conduits[channelId].numRequesters -= 1
                m.cleanChannel(channelId)
                m.mutex.Unlock()
                return true
        case <-ctx.Done():
                m.mutex.Lock()
                m.conduits[channelId].numRequesters -= 1
                m.cleanChannel(channelId)
                m.mutex.Unlock()
                return false
        }
}

func (m *ConduitManager) Respond(channelId string, ctx context.Context) (bool, PatchedRequest) {
        m.mutex.Lock()
        _, ok := m.conduits[channelId]
        if !ok {
                m.conduits[channelId] = NewConduit()
        }
        m.conduits[channelId].numResponders += 1
        channel := m.conduits[channelId].channel
        m.mutex.Unlock()

        select {
        case request := <-channel:
                m.mutex.Lock()
                m.conduits[channelId].numResponders -= 1
                m.cleanChannel(channelId)
                m.mutex.Unlock()
                return true, request
        case <-ctx.Done():
                m.mutex.Lock()
                m.conduits[channelId].numResponders -= 1
                m.cleanChannel(channelId)
                m.mutex.Unlock()
                return false, PatchedRequest{}
        }
}

func (m *ConduitManager) Switch(channelId string, request PatchedRequest) {
        m.mutex.Lock()
        _, ok := m.conduits[channelId]
        if !ok {
                m.conduits[channelId] = NewConduit()
        }
        m.conduits[channelId].numRequesters += 1
        channel := m.conduits[channelId].channel
        m.mutex.Unlock()

        channel <- request
        m.mutex.Lock()
        m.conduits[channelId].numRequesters -= 1
        m.cleanChannel(channelId)
        m.mutex.Unlock()
}

func (m *ConduitManager) cleanChannel(channelId string) {
        isDormant := m.conduits[channelId].numRequesters == 0 && m.conduits[channelId].numResponders == 0

        if isDormant {
                delete(m.conduits, channelId)
        }
}
