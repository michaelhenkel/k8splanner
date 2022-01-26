FROM debian:stretch

ADD https://go.dev/dl/go1.16.13.linux-amd64.tar.gz /
RUN tar -C /usr/local -xzf /go1.16.13.linux-amd64.tar.gz && rm /go1.16.13.linux-amd64.tar.gz
RUN mkdir -p /go/src
ADD https://github.com/go-task/task/releases/download/v3.10.0/task_linux_amd64.deb /
RUN dpkg -i /task_linux_amd64.deb && rm /task_linux_amd64.deb
RUN apt update -y && apt install -y git 
