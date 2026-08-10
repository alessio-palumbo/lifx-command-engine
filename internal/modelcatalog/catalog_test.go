package modelcatalog

import "testing"

func TestFind(t *testing.T) {
	model, err := Find("functiongemma-270m-it", "kaggle")
	if err != nil || model.Revision != "1" || model.Handle == "" {
		t.Fatalf("model=%#v err=%v", model, err)
	}
	if _, err := Find("unknown", "kaggle"); err == nil {
		t.Fatal("expected unknown model error")
	}
}
