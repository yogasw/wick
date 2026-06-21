PLUGIN_DIR ?= $(HOME)/.wick/plugins/connectors
PLUGINS    ?= slack googleworkspace echo

.PHONY: plugins
plugins: ## Build in-tree connector plugins into the runtime plugins dir
	@for name in $(PLUGINS); do \
		out="$(PLUGIN_DIR)/$$name"; \
		mkdir -p "$$out"; \
		echo "building plugin $$name -> $$out"; \
		go build -o "$$out/$$name" ./cmd/plugins/$$name || exit 1; \
		"$$out/$$name" --dump-manifest > "$$out/plugin.json" || exit 1; \
	done
	@echo "plugins built into $(PLUGIN_DIR)"
