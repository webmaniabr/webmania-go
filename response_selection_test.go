package webmania

import "testing"

func TestAlpha2ResponseSelection(t *testing.T) {
	cases := []struct {
		name      string
		responses map[string]any
		status    int
		want      any
		present   bool
	}{{"exact-precedes-class-and-default", map[string]any{"200": "exact", "2XX": "class", "default": "default"}, 200, "exact", true}, {"class-precedes-default", map[string]any{"2XX": "class", "default": "default"}, 201, "class", true}, {"declared-default", map[string]any{"default": "default"}, 201, "default", true}, {"no-implicit-class", map[string]any{"200": "exact"}, 201, nil, false}, {"different-class", map[string]any{"4XX": "error"}, 201, nil, false}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := responseForStatus(test.responses, test.status)
			if ok != test.present || got != test.want {
				t.Fatalf("response selection got %v %v", got, ok)
			}
		})
	}
}
