package passcli

import "testing"

func TestValidateURI(t *testing.T) {
	valid := []string{
		"pass://abc123==/def456",
		"pass://a-b_c/d-e_f==",
		"pass://share/item/username",
		"pass://share/item/My Field",
		"pass://share/item/Mot de passe éphémère",
		"pass://share/item/🔑",
	}
	invalid := []string{
		"",
		"http://share/item",
		"pass://share",
		"pass:///item",
		"pass://share//field",
		"pass://share/item/",
		"pass://share/item/   ",
		"pass://share/item/field/extra",
		"pass://sh are/item",
		"pass://../item",
		"pass://share/it;em",
		"pass://share/item/fi\nld",
		"pass://share/item/fi\x00ld",
	}

	for _, uri := range valid {
		if err := ValidateURI(uri); err != nil {
			t.Errorf("ValidateURI(%q) = %v, want nil", uri, err)
		}
	}
	for _, uri := range invalid {
		if err := ValidateURI(uri); err == nil {
			t.Errorf("ValidateURI(%q) = nil, want error", uri)
		}
	}
}
