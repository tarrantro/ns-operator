SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

MODULE       := github.com/tarrantro/ns-operator
BOILERPLATE  := $(CURDIR)/hack/boilerplate.go.txt
GENERATED_DIR := $(CURDIR)/pkg/generated
APIS_DIR     := $(CURDIR)/pkg/apis
CRD_DIR      := $(CURDIR)/config/crd

.PHONY: code-generate
code-generate: ## Regenerate deepcopy, clientset, lister and informer code under apis/ and pkg/generated
	go get -tool k8s.io/code-generator@v0.37.0
	mkdir -p "$(GENERATED_DIR)/clientset" "$(GENERATED_DIR)/listers" "$(GENERATED_DIR)/informers"
	CODEGEN_PKG=$$(go list -m -f '{{.Dir}}' k8s.io/code-generator); \
	source "$$CODEGEN_PKG/kube_codegen.sh"; \
	kube::codegen::gen_helpers \
		--boilerplate "$(BOILERPLATE)" \
		"$(APIS_DIR)"; \
	kube::codegen::gen_client \
		--with-watch \
		--output-dir "$(GENERATED_DIR)" \
		--output-pkg $(MODULE)/pkg/generated \
		--boilerplate "$(BOILERPLATE)" \
		"$(APIS_DIR)"

.PHONY: manifests
manifests: ## Generate CRD YAML under config/crd from kubebuilder markers
	go get -tool sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
	mkdir -p "$(CRD_DIR)"
	go tool controller-gen crd paths="$(APIS_DIR)/..." output:crd:dir="$(CRD_DIR)"

.PHONY: build
build: ## Build all packages
	CGO_ENABLED=0 go build -trimpath -o bin/ns-operator .

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: deploy
deploy: ## Deploy the operator to the current kubectl context
	kubectl apply -f config/manager/namespace.yaml
	kubectl apply -f config/crd/
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/deployment.yaml

.PHONY: e2e-test
e2e-test: ## Run e2e tests against the current kubectl context (requires `make deploy` first)
	go test -tags e2e ./test/e2e/... -v -timeout 5m

.PHONY: clean
clean: ## Remove everything deployed by `make deploy`
	kubectl delete -f config/manager/deployment.yaml --ignore-not-found
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete -f config/crd/ --ignore-not-found
	kubectl delete -f config/manager/namespace.yaml --ignore-not-found
