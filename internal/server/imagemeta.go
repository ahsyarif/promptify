package server

import "slices"

type option struct{ Value, Label string }

var imageModels = []option{
	{"midjourney", "Midjourney"},
	{"stable-diffusion", "Stable Diffusion"},
	{"dall-e", "DALL·E"},
	{"flux", "Flux"},
	{"other", "Other"},
}

var aspectRatios = []string{"1:1", "4:5", "5:4", "2:3", "3:2", "3:4", "4:3", "9:16", "16:9", "21:9"}

func validModel(v string) bool {
	return slices.ContainsFunc(imageModels, func(o option) bool { return o.Value == v })
}

func validAspectRatio(v string) bool { return v == "" || slices.Contains(aspectRatios, v) }

func modelLabel(v string) string {
	for _, o := range imageModels {
		if o.Value == v {
			return o.Label
		}
	}
	return v
}
