PLUGIN_DIR ?= $(HOME)/.wick/plugins/connectors
PLUGINS    ?= slack googleworkspace echo
VERSION    ?= 0.0.0-dev
WICK_PLUGIN_SIGNING_KEY ?=

.PHONY: plugins
plugins: ## Build in-tree connector plugins (signed if WICK_PLUGIN_SIGNING_KEY set)
	@for name in $(PLUGINS); do \
		out="$(PLUGIN_DIR)/$$name"; mkdir -p "$$out"; \
		echo "building plugin $$name -> $$out"; \
		go build -ldflags "-X github.com/yogasw/wick/pkg/plugin.Version=$(VERSION)" -o "$$out/$$name" ./cmd/plugins/$$name || exit 1; \
		if [ -n "$(WICK_PLUGIN_SIGNING_KEY)" ]; then \
			"$$out/$$name" --dump-manifest --sign-key "$(WICK_PLUGIN_SIGNING_KEY)" > "$$out/plugin.json" || exit 1; \
		else \
			"$$out/$$name" --dump-manifest > "$$out/plugin.json" || exit 1; \
		fi; \
	done
	@echo "plugins built into $(PLUGIN_DIR)"

.PHONY: plugin-keygen
plugin-keygen: ## Generate an ed25519 keypair for signing plugins
	@go run ./cmd/plugin-keygen
