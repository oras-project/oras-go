# TODO: use this for fetch-dist
TARGET_OBJS ?= todo1.txt todo2.txt

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
	! find pkg examples -name "*.go" -type f -exec file "{}" ";" | grep CRLF
	! find scripts -name "*.sh" -type f -exec file "{}" ";" | grep CRLF

.PHONY: fix-encoding
fix-encoding:
	find pkg examples -type f -name "*.go" -exec sed -i -e "s/\r//g" {} +
	find scripts -type f -name "*.sh" -exec sed -i -e "s/\r//g" {} +

.PHONY: vendor
vendor:
	GO111MODULE=on go mod vendor

# TODO: use this
.PHONY: fetch-dist
fetch-dist:
	mkdir -p _dist
	cd _dist && \
	for obj in ${TARGET_OBJS} ; do \
		curl -sSL -o oras_${VERSION}_$${obj} https://github.com/oras-project/oras-go/releases/download/v${VERSION}/oras_${VERSION}_$${obj} ; \
	done

# 1. mkdir _dist/
# 2. manually download .zip / .tar.gz from release page
# 3. move files into _dist/
# 4. make sign
# 5. upload .asc files back to release page
.PHONY: sign
sign:
	for f in $$(ls _dist/*.{zip,tar.gz} 2>/dev/null) ; do \
		gpg --armor --detach-sign $${f} ; \
	done

.PHONY: checksums
checksums:
	cd _dist/ && \
	for f in $$(ls *.{zip,tar.gz} 2>/dev/null) ; do \
		shasum -a 256 $${f} ; \
	done
