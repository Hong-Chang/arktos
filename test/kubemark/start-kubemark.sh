#!/usr/bin/env bash

# Copyright 2015 The Kubernetes Authors.
# Copyright 2020 Authors of Arktos - file modified.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Script that creates a Kubemark cluster for any given cloud provider.

set -o errexit
set -o nounset
set -o pipefail

TMP_ROOT="$(dirname "${BASH_SOURCE[@]}")/../.."
KUBE_ROOT=$(readlink -e "${TMP_ROOT}" 2> /dev/null || perl -MCwd -e 'print Cwd::abs_path shift' "${TMP_ROOT}")

source "${KUBE_ROOT}/test/kubemark/skeleton/util.sh"
source "${KUBE_ROOT}/test/kubemark/cloud-provider-config.sh"
source "${KUBE_ROOT}/test/kubemark/${CLOUD_PROVIDER}/util.sh"
source "${KUBE_ROOT}/cluster/kubemark/${CLOUD_PROVIDER}/config-default.sh"

if [[ -f "${KUBE_ROOT}/test/kubemark/${CLOUD_PROVIDER}/startup.sh" ]] ; then
  source "${KUBE_ROOT}/test/kubemark/${CLOUD_PROVIDER}/startup.sh"
fi

source "${KUBE_ROOT}/cluster/kubemark/util.sh"

KUBECTL="${KUBE_ROOT}/cluster/kubectl.sh"
KUBEMARK_DIRECTORY="${KUBE_ROOT}/test/kubemark"
RESOURCE_DIRECTORY="${KUBEMARK_DIRECTORY}/resources"
LOCAL_KUBECONFIG="${RESOURCE_DIRECTORY}/kubeconfig.kubemark"
LOCAL_KUBECONFIG_TMP="${RESOURCE_DIRECTORY}/kubeconfig.kubemark.tmp"

export SCALEOUT_TP_COUNT="${SCALEOUT_TP_COUNT:-1}"

### files to explicitly save the kubeconfig to different cluster or proxy
### those are used for targeted operations such as tenant creation etc on
### desired cluster directly
###
TP_KUBECONFIG="${RESOURCE_DIRECTORY}/kubeconfig.kubemark-tp"
PROXY_KUBECONFIG_SAVED="${RESOURCE_DIRECTORY}/kubeconfig.kubemark.proxy.saved"
RP_KUBECONFIG_SAVED="${RESOURCE_DIRECTORY}/kubeconfig.kubemark.rp.saved"
if [[ "${SCALEOUT_CLUSTER:-false}" == "false" ]]; then
  TP_KUBECONFIG="${LOCAL_KUBECONFIG}"
  LOCAL_KUBECONFIG_TMP="${LOCAL_KUBECONFIG}"
  RP_KUBECONFIG_SAVED="${LOCAL_KUBECONFIG}"
fi

export KUBERNETES_SCALEOUT_PROXY_APP=${KUBERNETES_SCALEOUT_PROXY_APP:-haproxy}
export PROXY_CONFIG_FILE=${PROXY_CONFIG_FILE:-"haproxy.cfg"}
export SCALEOUT_CLUSTER=${SCALEOUT_CLUSTER:-false}
export KUBE_APISERVER_EXTRA_ARGS=${KUBE_APISERVER_EXTRA_ARGS:-}
export KUBE_CONTROLLER_EXTRA_ARGS=${KUBE_CONTROLLER_EXTRA_ARGS:-}
export KUBE_SCHEDULER_EXTRA_ARGS=${KUBE_SCHEDULER_EXTRA_ARGS:-}

export PROXY_CONFIG_FILE_TMP="${RESOURCE_DIRECTORY}/${PROXY_CONFIG_FILE}.tmp"

# Generate a random 6-digit alphanumeric tag for the kubemark image.
# Used to uniquify image builds across different invocations of this script.
KUBEMARK_IMAGE_TAG=$(head /dev/urandom | tr -dc 'a-z0-9' | fold -w 6 | head -n 1)

# Create a docker image for hollow-node and upload it to the appropriate docker registry.
function create-and-upload-hollow-node-image {
  authenticate-docker
  KUBEMARK_IMAGE_REGISTRY="${KUBEMARK_IMAGE_REGISTRY:-${CONTAINER_REGISTRY}/${PROJECT}}"
  if [[ "${KUBEMARK_BAZEL_BUILD:-}" =~ ^[yY]$ ]]; then
    # Build+push the image through bazel.
    touch WORKSPACE # Needed for bazel.
    build_cmd=("bazel" "run" "//cluster/images/kubemark:push" "--define" "REGISTRY=${KUBEMARK_IMAGE_REGISTRY}" "--define" "IMAGE_TAG=${KUBEMARK_IMAGE_TAG}")
    run-cmd-with-retries "${build_cmd[@]}"
  else
    # Build+push the image through makefile.
    build_cmd=("make" "${KUBEMARK_IMAGE_MAKE_TARGET}")
    MAKE_DIR="${KUBE_ROOT}/cluster/images/kubemark"
    KUBEMARK_BIN="$(kube::util::find-binary-for-platform kubemark linux/amd64)"
    if [[ -z "${KUBEMARK_BIN}" ]]; then
      echo 'Cannot find cmd/kubemark binary'
      exit 1
    fi
    echo "Copying kubemark binary to ${MAKE_DIR}"
    cp "${KUBEMARK_BIN}" "${MAKE_DIR}"
    CURR_DIR=$(pwd)
    cd "${MAKE_DIR}"
    REGISTRY=${KUBEMARK_IMAGE_REGISTRY} IMAGE_TAG=${KUBEMARK_IMAGE_TAG} run-cmd-with-retries "${build_cmd[@]}"
    rm kubemark
    cd "$CURR_DIR"
  fi
  echo "Created and uploaded the kubemark hollow-node image to docker registry."
  # Cleanup the kubemark image after the script exits.
  if [[ "${CLEANUP_KUBEMARK_IMAGE:-}" == "true" ]]; then
    trap delete-kubemark-image EXIT
  fi
}

function delete-kubemark-image {
  delete-image "${KUBEMARK_IMAGE_REGISTRY}/kubemark:${KUBEMARK_IMAGE_TAG}"
}

# Generate secret and configMap for the hollow-node pods to work, prepare
# manifests of the hollow-node and heapster replication controllers from
# templates, and finally create these resources through kubectl.
function create-kube-hollow-node-resources {
  # Create kubemark namespace.
  "${KUBECTL}" create -f "${RESOURCE_DIRECTORY}/kubemark-ns.json"

  # Create configmap for configuring hollow- kubelet, proxy and npd.
  "${KUBECTL}" create configmap "node-configmap" --namespace="kubemark" \
    --from-literal=content.type="${TEST_CLUSTER_API_CONTENT_TYPE}" \
    --from-file=kernel.monitor="${RESOURCE_DIRECTORY}/kernel-monitor.json" \
    --from-literal=resource.server="${RESOURCE_SERVER:-}" \
    --from-literal=tenant.servers="${TENANT_SERVERS:-}"

  # Create secret for passing kubeconfigs to kubelet, kubeproxy and npd.
  # It's bad that all component shares the same kubeconfig.
  # TODO(https://github.com/kubernetes/kubernetes/issues/79883): Migrate all components to separate credentials.
  "${KUBECTL}" create secret generic "kubeconfig" --type=Opaque --namespace="kubemark" \
    --from-file=kubelet.kubeconfig="${TP_KUBECONFIG}" \
    --from-file=kubeproxy.kubeconfig="${TP_KUBECONFIG}" \
    --from-file=npd.kubeconfig="${RP_KUBECONFIG_SAVED}" \
    --from-file=heapster.kubeconfig="${TP_KUBECONFIG}" \
    --from-file=cluster_autoscaler.kubeconfig="${TP_KUBECONFIG}" \
    --from-file=dns.kubeconfig="${TP_KUBECONFIG}"

  # Create addon pods.
  # Heapster.
  mkdir -p "${RESOURCE_DIRECTORY}/addons"
  MASTER_IP=$(grep server "${LOCAL_KUBECONFIG_TMP}" | awk -F "/" '{print $3}')
  sed "s@{{MASTER_IP}}@${MASTER_IP}@g" "${RESOURCE_DIRECTORY}/heapster_template.json" > "${RESOURCE_DIRECTORY}/addons/heapster.json"
  metrics_mem_per_node=4
  metrics_mem=$((200 + metrics_mem_per_node*NUM_NODES))
  sed -i'' -e "s@{{METRICS_MEM}}@${metrics_mem}@g" "${RESOURCE_DIRECTORY}/addons/heapster.json"
  metrics_cpu_per_node_numerator=${NUM_NODES}
  metrics_cpu_per_node_denominator=2
  metrics_cpu=$((80 + metrics_cpu_per_node_numerator / metrics_cpu_per_node_denominator))
  sed -i'' -e "s@{{METRICS_CPU}}@${metrics_cpu}@g" "${RESOURCE_DIRECTORY}/addons/heapster.json"
  eventer_mem_per_node=500
  eventer_mem=$((200 * 1024 + eventer_mem_per_node*NUM_NODES))
  sed -i'' -e "s@{{EVENTER_MEM}}@${eventer_mem}@g" "${RESOURCE_DIRECTORY}/addons/heapster.json"

  # Cluster Autoscaler.
  if [[ "${ENABLE_KUBEMARK_CLUSTER_AUTOSCALER:-}" == "true" ]]; then
    echo "Setting up Cluster Autoscaler"
    KUBEMARK_AUTOSCALER_MIG_NAME="${KUBEMARK_AUTOSCALER_MIG_NAME:-${NODE_INSTANCE_PREFIX}-group}"
    KUBEMARK_AUTOSCALER_MIN_NODES="${KUBEMARK_AUTOSCALER_MIN_NODES:-0}"
    KUBEMARK_AUTOSCALER_MAX_NODES="${KUBEMARK_AUTOSCALER_MAX_NODES:-10}"
    NUM_NODES=${KUBEMARK_AUTOSCALER_MAX_NODES}
    echo "Setting maximum cluster size to ${NUM_NODES}."
    KUBEMARK_MIG_CONFIG="autoscaling.k8s.io/nodegroup: ${KUBEMARK_AUTOSCALER_MIG_NAME}"
    sed "s/{{master_ip}}/${MASTER_IP}/g" "${RESOURCE_DIRECTORY}/cluster-autoscaler_template.json" > "${RESOURCE_DIRECTORY}/addons/cluster-autoscaler.json"
    sed -i'' -e "s@{{kubemark_autoscaler_mig_name}}@${KUBEMARK_AUTOSCALER_MIG_NAME}@g" "${RESOURCE_DIRECTORY}/addons/cluster-autoscaler.json"
    sed -i'' -e "s@{{kubemark_autoscaler_min_nodes}}@${KUBEMARK_AUTOSCALER_MIN_NODES}@g" "${RESOURCE_DIRECTORY}/addons/cluster-autoscaler.json"
    sed -i'' -e "s@{{kubemark_autoscaler_max_nodes}}@${KUBEMARK_AUTOSCALER_MAX_NODES}@g" "${RESOURCE_DIRECTORY}/addons/cluster-autoscaler.json"
  fi

  # Kube DNS.
  if [[ "${ENABLE_KUBEMARK_KUBE_DNS:-}" == "true" ]]; then
    echo "Setting up kube-dns"
    sed "s@{{dns_domain}}@${KUBE_DNS_DOMAIN}@g" "${RESOURCE_DIRECTORY}/kube_dns_template.yaml" > "${RESOURCE_DIRECTORY}/addons/kube_dns.yaml"
  fi

  "${KUBECTL}" create -f "${RESOURCE_DIRECTORY}/addons" --namespace="kubemark"

  # Create the replication controller for hollow-nodes.
  # We allow to override the NUM_REPLICAS when running Cluster Autoscaler.
  NUM_REPLICAS=${NUM_REPLICAS:-${NUM_NODES}}
  if [[ "${SCALEOUT_CLUSTER:-false}" == "true" ]]; then
    sed "s@{{numreplicas}}@${NUM_REPLICAS}@g" "${RESOURCE_DIRECTORY}/hollow-node_template_scaleout.yaml" > "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  else
    sed "s@{{numreplicas}}@${NUM_REPLICAS}@g" "${RESOURCE_DIRECTORY}/hollow-node_template.yaml" > "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  fi
  proxy_cpu=20
  if [ "${NUM_NODES}" -gt 1000 ]; then
    proxy_cpu=50
  fi
  proxy_mem_per_node=50
  proxy_mem=$((100 * 1024 + proxy_mem_per_node*NUM_NODES))
  sed -i'' -e "s@{{HOLLOW_PROXY_CPU}}@${proxy_cpu}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{HOLLOW_PROXY_MEM}}@${proxy_mem}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{kubemark_image_registry}}@${KUBEMARK_IMAGE_REGISTRY}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{kubemark_image_tag}}@${KUBEMARK_IMAGE_TAG}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{master_ip}}@${RESOURCE_SERVER:-}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{hollow_kubelet_params}}@${HOLLOW_KUBELET_TEST_ARGS}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{hollow_proxy_params}}@${HOLLOW_PROXY_TEST_ARGS}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{kubemark_mig_config}}@${KUBEMARK_MIG_CONFIG:-}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{tenant_servers}}@${TENANT_SERVERS:-}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  sed -i'' -e "s@{{resource_server}}@${RESOURCE_SERVER:-}@g" "${RESOURCE_DIRECTORY}/hollow-node.yaml"
  "${KUBECTL}" create -f "${RESOURCE_DIRECTORY}/hollow-node.yaml" --namespace="kubemark"

  echo "Created secrets, configMaps, replication-controllers required for hollow-nodes."
}

# Wait until all hollow-nodes are running or there is a timeout.
function wait-for-hollow-nodes-to-run-or-timeout {
  timeout_seconds=$1
  echo -n "Waiting for all hollow-nodes to become Running"
  start=$(date +%s)
  nodes=$("${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get node 2> /dev/null) || true
  ready=$(($(echo "${nodes}" | grep -vc "NotReady") - 1))

  until [[ "${ready}" -ge "${NUM_REPLICAS}" ]]; do
    echo -n "."
    sleep 1
    now=$(date +%s)
    if [ $((now - start)) -gt ${timeout_seconds:-1800} ]; then
      echo ""
      # shellcheck disable=SC2154 # Color defined in sourced script
      echo -e "${color_red} Timeout waiting for all hollow-nodes to become Running. ${color_norm}"
      # Try listing nodes again - if it fails it means that API server is not responding
      if "${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get node &> /dev/null; then
        echo "Found only ${ready} ready hollow-nodes while waiting for ${NUM_NODES}."
      else
        echo "Got error while trying to list hollow-nodes. Probably API server is down."
      fi
      pods=$("${KUBECTL}" get pods -l name=hollow-node --namespace=kubemark) || true
      running=$(($(echo "${pods}" | grep -c "Running")))
      echo "${running} hollow-nodes are reported as 'Running'"
      not_running=$(($(echo "${pods}" | grep -vc "Running") - 1))
      echo "${not_running} hollow-nodes are reported as NOT 'Running'"
      echo "${pods}" | grep -v Running
      exit 1
    fi
    nodes=$("${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get node 2> /dev/null) || true
    ready=$(($(echo "${nodes}" | grep -vc "NotReady") - 1))
  done
  # shellcheck disable=SC2154 # Color defined in sourced script
  echo -e "${color_green} Done!${color_norm}"
}

############################### Main Function ########################################

# Setup for hollow-nodes.
function start-hollow-nodes {
  # shellcheck disable=SC2154 # Color defined in sourced script
  echo -e "${color_yellow}STARTING SETUP FOR HOLLOW-NODES${color_norm}"
  create-and-upload-hollow-node-image
  if [ "${CLOUD_PROVIDER}" = "aws" ]; then
    (
      MASTER_IP=${MASTER_INTERNAL_IP}
      create-kube-hollow-node-resources
    )
  else
    create-kube-hollow-node-resources
  fi

  # the timeout value is set based on default QPS of 20/sec and a buffer time of 10 minutes
  let timeout_seconds=${KUBEMARK_NUM_NODES:-10}/20+600
  echo -e "$(date): Wait ${timeout_seconds} seconds for ${KUBEMARK_NUM_NODES:-10} hollow nodes to be ready."

  wait-for-hollow-nodes-to-run-or-timeout ${timeout_seconds}
}

detect-project &> /dev/null

rm /tmp/saved_tenant_ips.txt >/dev/null 2>&1 || true

if [[ "${SCALEOUT_CLUSTER:-false}" == "true" ]]; then
  export KUBE_ENABLE_APISERVER_INSECURE_PORT=true
  export KUBERNETES_TENANT_PARTITION=true
  export KUBERNETES_SCALEOUT_PROXY=true

  for (( tp_num=1; tp_num<=${SCALEOUT_TP_COUNT}; tp_num++ ))
  do
    export TENANT_PARTITION_SEQUENCE=${tp_num}
    create-kubemark-master

    MASTER_IP=$(grep server "${LOCAL_KUBECONFIG_TMP}" | awk -F "/" '{print $3}')
    export PROXY_RESERVED_IP=$(echo ${MASTER_IP} | cut -d: -f1)
    echo "DBG: PROXY_RESERVED_IP=$PROXY_RESERVED_IP"

    TP_SERVER="http://$(cat /tmp/master_reserved_ip.txt):8080"
    if [[ ${tp_num} == 1 ]]; then
      export TENANT_SERVERS=${TP_SERVER}
    else
      export TENANT_SERVERS="${TENANT_SERVERS},${TP_SERVER}"
    fi

    cp -f ${LOCAL_KUBECONFIG_TMP} "${RESOURCE_DIRECTORY}/kubeconfig.kubemark.tp-${tp_num}.direct"
    sed -i -e "s@http://${PROXY_RESERVED_IP}:8888@${TP_SERVER}@g" "${RESOURCE_DIRECTORY}/kubeconfig.kubemark.tp-${tp_num}.direct"
    export KUBERNETES_SCALEOUT_PROXY=false
  done
  echo "DBG: tenant-servers:  ${TENANT_SERVERS}"
fi

if [[ "${SCALEOUT_CLUSTER:-false}" == "true" ]]; then
  echo "DBG: set tenant partition flag false"
  export KUBERNETES_TENANT_PARTITION=false
  export KUBERNETES_RESOURCE_PARTITION=true
  create-kubemark-master
  export KUBERNETES_RESOURCE_PARTITION=false
  export KUBERNETES_SCALEOUT_PROXY=false

  export RESOURCE_SERVER="http://"$(grep server "${LOCAL_KUBECONFIG_TMP}" | awk -F "/" '{print $3}')
  echo "DBG: resource-server: " ${RESOURCE_SERVER}
  cp -f ${LOCAL_KUBECONFIG_TMP} ${RP_KUBECONFIG_SAVED}
else
  create-kubemark-master
fi

# start hollow nodes with multiple tenant partition parameters
#
start-hollow-nodes

echo ""
if [ "${CLOUD_PROVIDER}" = "aws" ]; then
  echo "Master Public IP: ${MASTER_PUBLIC_IP}"
  echo "Master Internal IP: ${MASTER_INTERNAL_IP}"
else
  echo "Master IP: ${MASTER_IP}"
fi
echo "Kubeconfig for kubemark master is written in ${LOCAL_KUBECONFIG_TMP}"

# all kubectl OPs go via the proxy
#
### internally the TP configures are set with proxy URL, simply copy for POC phase for now
###
if [[ "${SCALEOUT_CLUSTER:-false}" == "true" ]]; then
  cp -f ${TP_KUBECONFIG} ${PROXY_KUBECONFIG_SAVED}
fi

sleep 5
echo -e "\nListing kubeamrk cluster details:" >&2
echo -e "Getting total nodes number:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get node | wc -l
echo
echo -e "Getting total hollow-nodes number:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get node | grep "hollow-node" | wc -l
echo
echo -e "Getting endpoints status:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get endpoints -A
echo
echo -e "Getting workload controller co status:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get co
echo
echo -e "Getting apiserver data partition status:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get datapartition
echo
echo -e "Getting ETCD data partition status:" >&2
"${KUBECTL}" --kubeconfig="${TP_KUBECONFIG}" get etcd
echo