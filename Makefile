.PHONY: up down logs test loadtest crash-test

up:            ## start the whole stack
	docker compose up --build -d
	@echo "Dashboard http://localhost:3000 | API http://localhost:8000/docs | Grafana http://localhost:3001"

down:          ## stop everything (add -v manually to delete data)
	docker compose down

logs:
	docker compose logs -f ingest writer api

test:          ## run all unit tests
	cd ingest-service && go test ./...
	cd api && pytest -q
	cd dashboard && npm run build

loadtest:      ## ~5,000 msg/s for 60 seconds against the running stack
	docker compose stop simulator
	cd simulator && (python simulator.py --vehicles 300 --interval 0.1 --duration 60 & \
	                 python simulator.py --vehicles 300 --interval 0.1 --duration 60 --id-offset 300; wait)

crash-test:    ## stop the writer for 30s, then restart it and watch it catch up
	docker compose stop writer
	sleep 30
	docker compose start writer
