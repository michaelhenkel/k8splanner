#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -x

PWD=$(pwd)
export GOPATH=$(go env GOPATH)
MOD=${MOD:-"ssd-git.juniper.net/contrail/cn2/deployer"}
HEADER=${HEADER-"${PWD}/$(dirname $0)/boilerplate.go.txt"}
GOSRC="$(go env GOSRC)"
gosrc="${GOSRC:-${GOPATH}/src}"
GOBIN="$(go env GOBIN)"
gobin="${GOBIN:-${GOPATH}/bin}"

echo ${gobin}

# required source pacjaked
CODEGEN_PKG=${gosrc}/k8s.io/code-generator
API_PKG=${gosrc}/k8s.io/api
APIMACHINERY_PKG=${gosrc}/k8s.io/apimachinery
PROTOBUF_PKG=${gosrc}/github.com/gogo/protobuf

# get required sources
(
    mkdir -p ${gosrc}
    cd ${gosrc}
    go get -d k8s.io/code-generator || true
    go get -d k8s.io/api || true
    go get -d k8s.io/apimachinery || true
    go get -d github.com/gogo/protobuf || true
)

# install generators
(
    cd ${CODEGEN_PKG}
    git checkout tags/v0.22.0-alpha.0
    git status
    go install ./cmd/{defaulter-gen,conversion-gen,client-gen,lister-gen,informer-gen,deepcopy-gen,openapi-gen,go-to-protobuf,go-to-protobuf/protoc-gen-gogo}
)


export PATH=${PATH}:${gobin}
args="--go-header-file ${HEADER}"
# using go mod to generate code takes musch more longer to generate code
# 27min vs 20sec
if [[ "${PWD}" == ${GOPATH}* ]]; then
    # use source and pin to the appropriate version
    export GO111MODULE=off
    git -C ${API_PKG} fetch --all
    git -C ${APIMACHINERY_PKG} fetch --all
    git -C ${PROTOBUF_PKG} fetch --all
    git -C ${API_PKG} checkout v0.20.2
    git -C ${APIMACHINERY_PKG} checkout v0.20.2
    git -C ${PROTOBUF_PKG} checkout v1.3.1
else
    export GO111MODULE=on
    temp_output=$(mktemp -d)
    args+=" --output-base ${temp_output}"
fi

echo ${args}
#exit 0
# generate API registry

${gobin}/apiregister-gen \
    --input-dirs ${MOD}/pkg/apis/... \
    ${args}
rm -rf plugin/

${gobin}/conversion-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/configplane \
    --input-dirs ${MOD}/pkg/apis/common \
    --input-dirs ${MOD}/pkg/apis/controlplane \
    --input-dirs ${MOD}/pkg/apis/dataplane \
    -O zz_generated.conversion \
    --extra-peer-dirs k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/conversion,k8s.io/apimachinery/pkg/runtime \
    ${args}
${gobin}/deepcopy-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/configplane \
    --input-dirs ${MOD}/pkg/apis/common \
    --input-dirs ${MOD}/pkg/apis/controlplane \
    --input-dirs ${MOD}/pkg/apis/dataplane \
    -O zz_generated.deepcopy \
    ${args}
${gobin}/defaulter-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/configplane \
    --input-dirs ${MOD}/pkg/apis/common \
    --input-dirs ${MOD}/pkg/apis/controlplane \
    --input-dirs ${MOD}/pkg/apis/dataplane \
    -O zz_generated.defaults \
    --extra-peer-dirs k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/conversion,k8s.io/apimachinery/pkg/runtime \
    ${args}

# generate OpenAPI definitions
${gobin}/openapi-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    -i k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/api/resource,k8s.io/apimachinery/pkg/version,k8s.io/apimachinery/pkg/runtime,k8s.io/apimachinery/pkg/util/intstr,k8s.io/api/core/v1,k8s.io/api/apps/v1 \
    --report-filename /dev/null \
    --output-package ${MOD}/pkg/openapi \
    ${args}

${gobin}/client-gen \
    --input-base ${MOD}/pkg/apis \
    --input configplane/v1alpha1,controlplane/v1alpha1,dataplane/v1alpha1 \
    --clientset-path ${MOD}/pkg/client/clientset_generated \
    --clientset-name clientset \
    ${args}
${gobin}/lister-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --output-package ${MOD}/pkg/client/listers_generated \
    ${args}
${gobin}/informer-gen \
    --input-dirs ${MOD}/pkg/apis/configplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/common/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/controlplane/v1alpha1 \
    --input-dirs ${MOD}/pkg/apis/dataplane/v1alpha1 \
    --output-package ${MOD}/pkg/client/informers_generated \
    --listers-package ${MOD}/pkg/client/listers_generated \
    --versioned-clientset-package ${MOD}/pkg/client/clientset_generated/clientset \
    ${args}

if [[ "${PWD}" == ${GOPATH}* ]]; then
    # put back the source repo to the precedent commit
    git -C ${API_PKG} checkout -
    git -C ${APIMACHINERY_PKG} checkout -
    git -C ${PROTOBUF_PKG} checkout -
else
    cp -R ${temp_output}/${MOD}/pkg/* ${PWD}/pkg
    # needed until we can generate protobuf outside the go path
    git checkout -- pkg/apis/configplane/v1alpha1/zz_generated.api.register.go
    git checkout -- pkg/apis/controlplane/v1alpha1/zz_generated.api.register.go
    git checkout -- pkg/apis/dataplane/v1alpha1/zz_generated.api.register.go
fi
