docker compose -f docker-compose-bap.yml down -v
docker compose -f docker-compose-bpp.yml down -v
docker compose -f docker-compose-bpp-with-sandbox.yml down -v
docker compose -f docker-compose-gateway.yml down -v
docker compose -f docker-compose-registry.yml down -v
docker compose -f docker-compose-app.yml down -v
docker volume rm registry_data_volume registry_database_volume registry_logs_volume gateway_data_volume gateway_database_volume bap_client_config_volume bap_network_config_volume bpp_client_config_volume bpp_network_config_volume