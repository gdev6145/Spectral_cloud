# Mesh Node

The fundamental building block of the Spectral-Cloud mesh network.

## Overview

Each node in the mesh:
- Registers itself on the network
- Discovers other peers
- Routes traffic intelligently
- Maintains health status
- Participates in distributed consensus

## Node Types

- **Edge Node**: Consumer hardware, variable connectivity
- **Gateway Node**: Satellite backhaul interface
- **Relay Node**: High-availability routing nodes
- **AI Node**: Hosts sovereign AI agents

## Components

- `node.go` - Core node implementation
- `registry.go` - Local and distributed registry
- `health.go` - Health monitoring
- `lifecycle.go` - Node startup/shutdown

---