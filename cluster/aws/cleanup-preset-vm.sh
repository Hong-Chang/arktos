#!/bin/bash

# Copyright 2020 Authors of Arktos.
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

set -o errexit
set -o nounset
set -o pipefail

KUBE_VER=""
KUBEMARK_MASTER=${KUBEMARK_MASTER:-false}

# this scipt is to clean up arktos in the preset machine

function set-kube-env() {
  local kube_env_yaml="/etc/kubernetes/kube_env.yaml"

  # kube-env has all the environment variables we care about, in a flat yaml format
  eval "$(python -c '
import pipes,sys,yaml

for k,v in yaml.load(sys.stdin).iteritems():
  print("""readonly {var}={value}""".format(var = k, value = pipes.quote(str(v))))
  print("""export {var}""".format(var = k))
  ' < """${kube_env_yaml}""")"
}

function stop-flannel-ds {
  pushd /tmp
  wget https://raw.githubusercontent.com/coreos/flannel/master/Documentation/kube-flannel.yml
  kubectl --kubeconfig=$HOME/.kube/config delete -f /tmp/kube-flannel.yml || true
  rm -rf /tmp/kube-flannel.yml
  popd
}

function stop-containers {
  kubeadm reset -f
  docker system prune -f
  docker stop $(docker ps -aq) || true
  docker rmi $(docker images -q)
}

function remove-packages() {
  apt-get autoremove -y -q kubernetes-cni cri-tools
}

####################################################################################

echo "== cleanup starting =="

set-kube-env

stop-flannel-ds || true
stop-containers || true
remove-packages

sudo rm -rf /etc/motd
sudo rm -rf /etc/systemd/system/multi-user.target.wants/kubelet.service
sudo rm -rf /etc/kubernetes
sudo rm -rf /etc/srv/kubernetes
sudo rm -rf /etc/systemd/system/kubelet.service.d
sudo rm -rf /home/ubuntu/.kube/
sudo rm -rf /lib/systemd/system/kubelet.service
sudo rm -rf /mnt/master-pd
sudo rm -rf /root/.kube
sudo rm -rf /srv/kubernetes
sudo rm -rf /tmp/bootstrap-script  
sudo rm -rf /tmp/kubernetes-server-linux-amd64.tar.gz
sudo rm -rf /tmp/master-user-data  
sudo rm -rf /usr/bin/kubeadm
sudo rm -rf /usr/bin/kubectl
sudo rm -rf /usr/bin/kubelet
sudo rm -rf /usr/libexec/kubernetes
sudo rm -rf /usr/libexec/kubernetes/kubelet-plugins
sudo rm -rf /usr/local/bin/kube-proxy
sudo rm -rf /usr/share/sosreport/sos/plugins/__pycache__/kubernetes.cpython-35.pyc
sudo rm -rf /usr/share/sosreport/sos/plugins/kubernetes.py  
sudo rm -rf /usr/bin/kubelet
sudo rm -rf /var/log/containers/* 
sudo rm -rf /var/log/workload-controller-manager.log 
sudo rm -rf /var/cache/kubernetes-install
sudo rm -rf /var/log/pods  
sudo rm -rf /usr/share/sosreport/sos/plugins/__pycache__/etcd.cpython-35.pyc
sudo rm -rf /usr/share/sosreport/sos/plugins/etcd.py
sudo rm -rf /var/lib/etcd
sudo rm -rf /var/etcd 

echo "== cleanup done =="
