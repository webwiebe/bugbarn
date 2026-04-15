SHELL := /bin/bash

ANSIBLE_ROLES_PATH ?= ../rapid-root/infra/roles
INVENTORY ?= inventory.ini

setup-runners:
	ANSIBLE_ROLES_PATH=$(ANSIBLE_ROLES_PATH) ansible-playbook -i $(INVENTORY) github-runner.yml

setup-runners-macos:
	ANSIBLE_ROLES_PATH=$(ANSIBLE_ROLES_PATH) ansible-playbook -i $(INVENTORY) github-runner-macos.yml -K

ping:
	ansible -i $(INVENTORY) -m ping runners
	ansible -i $(INVENTORY) -m ping macos_runners

help:
	@echo "Available targets:"
	@echo "  setup-runners        Provision Linux GitHub Actions runners"
	@echo "  setup-runners-macos  Provision macOS GitHub Actions runners"
	@echo "  ping                 Test runner host connectivity"

