package translate

import "testing"

func TestTranslate(t *testing.T) {
	cases := map[string]string{
		"Halfvolle melk":            "Semi-skimmed milk",
		"Volkorenbrood":             "Wholemeal bread",
		"Kipfilet":                  "Chicken breast",
		"Kippensoep":                "Chicken soup",
		"Jonge kaas":                "Young cheese",
		"Gouda jong 48+":            "Gouda young 48+",
		"Zure room":                 "Sour cream",
		"Scharreleieren 10 st":      "Free-range eggs 10 pieces",
		"Appels Elstar":             "Apples Elstar",
		"Bananen":                   "Bananas",
		"Worteltjes":                "Baby carrots",
		"Gerookte zalm":             "Smoked salmon",
		"Rundergehakt":              "Beef mince",
		"Wc papier 8 rollen":        "Toilet paper 8 rolls",
		"Haverdrank barista":        "Oat drink barista",
		"Crème fraîche":             "Crème fraîche",
		"Biologische volle yoghurt": "Organic whole yoghurt",
		"Tomatensoep":               "Tomato soup",
		"Aardbeienjam":              "Strawberry jam",
		"Pindakaas":                 "Peanut butter",
		"ah halfvol melk 1,5l":      "Ah semi-skimmed milk 1,5l",
		"Kaas, jong":                "Cheese, young",
	}
	for in, want := range cases {
		got := Translate(in)
		if got.Text != want {
			t.Errorf("Translate(%q) = %q (known %d/%d), want %q", in, got.Text, got.Known, got.Words, want)
		}
		if !got.OK() {
			t.Errorf("Translate(%q) should be usable: %+v", in, got)
		}
	}
}

func TestTranslateUnknownPassesThrough(t *testing.T) {
	r := Translate("Unox Cup-a-Soup")
	if r.OK() || r.Known != 0 {
		t.Errorf("unknown words should not count as a translation: %+v", r)
	}
	if r.Text != "Unox Cup-a-Soup" {
		t.Errorf("text = %q", r.Text)
	}
}
