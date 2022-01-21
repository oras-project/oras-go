.PHONY: test
test: vendor check-encoding
	./scripts/test.sh

.PHONY: covhtml
covhtml:
	open .cover/coverage.html

.PHONY: acceptance
acceptance:
	./scripts/acceptance.sh

.PHONY: clean
clean:
	git status --ignored --short | grep '^!! ' | sed 's/!! //' | xargs rm -rf

.PHONY: check-encoding
check-encoding:
	! find . -name "*.go" -type f -exec file "{}" ";" | grep CRLF
	! find scripts -name "*.sh" -type f -exec file "{}" ";" | grep CRLF

.PHONY: fix-encoding
fix-encoding:
	find . -type f -name "*.go" -exec sed -i -e "s/\r//g" {} +
	find scripts -type f -name "*.sh" -exec sed -i -e "s/\r//g" {} +

.PHONY: vendor
vendor:
	go mod vendor
