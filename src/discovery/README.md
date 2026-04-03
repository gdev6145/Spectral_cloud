# Peer Discovery

Service for discovering and connecting to other mesh nodes.

## Discovery Methods

1. **mDNS (Multicast DNS)** - Local network discovery
2. **DHT (Distributed Hash Table)** - Extended network discovery
3. **Bootstrap Nodes** - Known entry points for new nodes
4. **Satellite Beacons** - Discovery via satellite infrastructure

## Interface

```
Discovery Service
├── Local Discovery (mDNS)
├── Remote Discovery (DHT)
├── Bootstrap Connection
└── Beacon Reception
```
