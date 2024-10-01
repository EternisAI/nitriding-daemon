FROM golang:1.22 AS builder

WORKDIR /nitriding-daemon

COPY . .
# Clone the repository and build the stand-alone nitriding executable.
# RUN git clone https://github.com/brave/nitriding-daemon.git
ARG TARGETARCH=amd64
ARG OS=linux
RUN ARCH=${TARGETARCH} OS=${OS} make -C /nitriding-daemon nitriding
RUN chmod +x /nitriding-daemon/nitriding
RUN mv /nitriding-daemon/nitriding /bin/

ENTRYPOINT [ "nitriding" ]
