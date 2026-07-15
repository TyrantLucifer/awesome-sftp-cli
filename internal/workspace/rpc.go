package workspace

type ListRequest struct{}

type ListResponse struct {
	Workspaces []Summary `json:"workspaces"`
}

type LoadRequest struct {
	Name string `json:"name"`
}

type LoadResponse struct {
	Document Document `json:"document"`
}

type SaveRequest struct {
	Name     string   `json:"name"`
	Document Document `json:"document"`
}

type SaveResponse struct{}
