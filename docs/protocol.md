# Mesh Network Protocol Specification

## Overview
This document outlines the specifications for a mesh network protocol designed for efficient communication and data transfer among nodes.

## Message Formats
The protocol supports various message formats, including but not limited to:
1. **Data Message**: Used to transmit data.
   - **Header**: 
     - `msg_type` (uint8): Type of message (Data, Acknowledgment, etc.)
     - `source_id` (uint32): Unique identifier for the sender node.
     - `destination_id` (uint32): Unique identifier for the receiver node.
    - `timestamp` (uint64): Unix timestamp when the message was sent.
    - `tenant_id` (string): Tenant identifier for multi-tenant isolation.
   - **Payload**: The actual data being transmitted.

2. **Control Message**: Used for managing node states.
   - **Header**: 
     - `msg_type` (uint8)
     - `control_type` (uint8): Type of control message (Heartbeat, Handshake, etc.)
    - `node_id` (uint32): Identifier of the node involved.
    - `tenant_id` (string): Tenant identifier for multi-tenant isolation.
   - **Payload**: Control-specific data (if any).

## Node Handshake Sequences
Nodes in the network must perform a handshake to establish a connection. The sequence is as follows:
1. **Handshake Request**: A node sends a handshake request to another node.
2. **Handshake Response**: The receiving node responds with an acknowledgment.
3. **Complete Handshake**: Upon receiving the acknowledgment, the initiating node confirms the handshake.

## Routing Protocol Details
The routing protocol is based on a dynamic routing algorithm that includes:
- **Route Discovery**: Nodes discover routes through broadcasting requests in the network.
- **Route Maintenance**: Links are monitored, and new routes are established as necessary to maintain connectivity.
- **Routing Metrics**: Metrics such as latency and hop count are used to select optimal paths.

## Serialization Using Protocol Buffers
Messages are serialized using Protocol Buffers for efficient transmission. The .proto file format includes:

```protobuf
syntax = "proto3";

message DataMessage {
    enum MsgType {
        DATA = 0;
        ACK = 1;
    }
    MsgType msg_type = 1;
    uint32 source_id = 2;
    uint32 destination_id = 3;
    int64 timestamp = 4;
    bytes payload = 5;
    string tenant_id = 6;
}

message ControlMessage {
    enum ControlType {
        HEARTBEAT = 0;
        HANDSHAKE = 1;
    }
    MsgType msg_type = 1;
    ControlType control_type = 2;
    uint32 node_id = 3;
    bytes payload = 4;
    string tenant_id = 5;
}

message Ack {
    uint32 source_id = 1;
    uint32 destination_id = 2;
    int64 timestamp = 3;
    string message = 4;
    string tenant_id = 5;
}

service MeshService {
    rpc SendData (DataMessage) returns (Ack);
    rpc SendControl (ControlMessage) returns (Ack);
}
}
```
