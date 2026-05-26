package network

import "testing"

func Test_pickMutation(t *testing.T) {
	seed := int64(12346)
	mutations := []mutation{
		{name: "DropMessage", fn: func() {}},
		{name: "DelayMessage", fn: func() {}},
	}
	chosen1 := pickMutation(mutations, seed)
	chosen2 := pickMutation(mutations, seed)

	if chosen1 != chosen2 {
		t.Errorf("Expected the same mutation to be chosen for the same seed, but got different mutations: %v and %v", chosen1, chosen2)
	}
}
