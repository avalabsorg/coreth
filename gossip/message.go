// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package gossip

type PullGossipRequest struct {
	Filter []byte `serialize:"true"`
}

type PullGossipResponse struct {
	GossipBytes [][]byte `serialize:"true"`
}
