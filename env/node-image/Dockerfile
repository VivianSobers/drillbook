# A kind node image plus sshd and python3, so Ansible can manage it over SSH
# the way it manages any on-prem machine. kubeadm, kubelet and containerd come
# from the kind image; Ansible builds the cluster with them.
FROM kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-server python3 \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /root/.ssh && chmod 700 /root/.ssh \
 && sed -i 's/^#\?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config \
 && systemctl enable ssh
