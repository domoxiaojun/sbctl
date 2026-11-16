package sbctl

// Sections of the sing-box configuration that can be replaced as a whole.
// Inbounds belong to protocol management. Outbounds carry credentials, so they
// are listed without them and are not edited here.
var configSections = []string{"log", "dns", "ntp", "route"}

const maxConfigSection = 200 * 1024

type ConfigChange struct {
	BaseHash string `json:"base_hash"`
	Section  string `json:"section"`
	Content  M      `json:"content"`
}

func (c ConfigChange) Validate() {
	if !hashRE.MatchString(c.BaseHash) || !contains(configSections, c.Section) || c.Content == nil {
		fail("INVALID_REQUEST", "配置片段或版本无效", 422)
	}
	if len(canonical(c.Content)) > maxConfigSection {
		fail("INVALID_REQUEST", "配置片段超过 200 KiB", 422)
	}
}

// configView is what an administrator edits: the replaceable sections in full
// and the names they may refer to. Its hash changes only with those sections,
// so user authorization does not invalidate an open editor.
func configView(nodeID string, config M) M {
	sections := M{}
	for _, name := range configSections {
		sections[name] = mapping(config[name])
	}
	outbounds := []M{}
	for _, raw := range array(config["outbounds"]) {
		o := mapping(raw)
		outbounds = append(outbounds, M{"tag": str(o["tag"]), "type": str(o["type"]), "server": str(o["server"]), "server_port": integer(o["server_port"])})
	}
	inbounds := []string{}
	for _, raw := range array(config["inbounds"]) {
		if tag := str(mapping(raw)["tag"]); tag != "" {
			inbounds = append(inbounds, tag)
		}
	}
	return M{"node_id": nodeID, "hash": digest(sections), "checked_at": now(), "sections": sections, "outbounds": outbounds, "inbound_tags": inbounds}
}

// planConfig replaces one section in the running configuration and in the
// template it is rendered from. Callers keep the original bytes for rollback.
func planConfig(config, template M, c ConfigChange) (M, M) {
	c.Validate()
	config, template = object(canonical(config)), object(canonical(template))
	if configView("", config)["hash"] != c.BaseHash {
		fail("CONFIG_CHANGED", "配置已变化，请重新读取后操作", 409)
	}
	for _, document := range []M{config, template} {
		if len(c.Content) == 0 {
			delete(document, c.Section)
		} else {
			document[c.Section] = object(canonical(c.Content))
		}
	}
	return config, template
}

// ApplyConfig runs a section change through the same durable plan, restart,
// probe verification and rollback as a protocol change.
func (m NodeMaintenance) ApplyConfig(id string, c ConfigChange) M {
	c.Validate()
	return m.apply(id, nil, &c)
}
