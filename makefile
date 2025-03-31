# Define the list of plugins
PLUGIN_NAMES = signer router secretskeymanager publisher redis reqpreprocessor schemavalidator signvalidator

.PHONY: install-plugins
install-plugins:
ifeq ($(strip $(PLUGIN_NAMES)),)
	@echo "PLUGIN_NAMES is empty. No plugins to install."
else
	./scripts/install-plugin-gcs.sh $(PLUGIN_NAMES)
endif