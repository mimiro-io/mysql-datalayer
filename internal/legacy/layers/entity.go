package layers

import (
	"strings"
)

type Entity struct {
	ID         string                 `json:"id"`
	IsDeleted  bool                   `json:"deleted"`
	References map[string]interface{} `json:"refs"`
	Properties map[string]interface{} `json:"props"`
}

// NewEntity Create a new entity with global uuid and internal resource id
func NewEntity() *Entity {
	e := Entity{}
	e.Properties = make(map[string]interface{})
	e.References = make(map[string]interface{})
	return &e
}

func (entity *Entity) StripProps() map[string]interface{} {

	var singleMap = make(map[string]interface{})
	for e, _ := range entity.Properties {
		singleMap[strings.SplitAfter(e, ":")[1]] = entity.Properties[e]
	}

	return singleMap
}
