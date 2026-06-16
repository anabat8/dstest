package scheduling

import "testing"

func Test_pickMutation(t *testing.T) {
	seed := int64(12346)
	mutations := []mutation{
		{Name: "DropMessage", fn: func() {}},
		{Name: "DelayMessage", fn: func() {}},
	}
	chosen1 := pickMutation(mutations, seed)
	chosen2 := pickMutation(mutations, seed)

	if chosen1.Name != chosen2.Name {
		t.Errorf("Expected the same mutation to be chosen for the same seed, but got different mutations: %v and %v", chosen1, chosen2)
	}
}
