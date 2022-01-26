ARG REGISTRY=svl-artifactory.juniper.net/atom-docker/cn2
ARG TAG=distroless
FROM ${REGISTRY}/base-builder:${TAG}
SHELL ["/bin/bash", "-c"]

# Put the source directories under tf-dev-env
COPY src/third_party /tf-dev-env/third_party

RUN cd /tf-dev-env/third_party && \
    ./fetch_packages.py --site-mirror https://svl-artifactory.juniper.net/artifactory
RUN --mount=type=cache,id=ccache,target=/root/.ccache \
    cd /tf-dev-env/third_party/log4cplus-1.1.1 && \
    ./configure --libdir /usr/lib/x86_64-linux-gnu/ --includedir /usr/include CXXFLAGS="-std=c++11" && \
    make -j$(grep -c ^processor /proc/cpuinfo) && \
    make install && \
    cp src/.libs/liblog4cplus*so* /usr/lib/x86_64-linux-gnu && \
    cp src/.libs/liblog4cplus*so* /libs/usr/lib/x86_64-linux-gnu && \
    ccache --show-stats
RUN --mount=type=cache,id=ccache,target=/root/.ccache \
    cd tf-dev-env/third_party/tbb-2018_U5 && \
    make -j$(grep -c ^processor /proc/cpuinfo) && \
    cp ./build/linux_*_release/*.so* /usr/lib/x86_64-linux-gnu && \
    cp ./build/linux_*_release/*.so* /libs/usr/lib/x86_64-linux-gnu && \
    ccache --show-stats
RUN cd /libs && \
    tar czvf third_party_libs.tgz usr && \
    cp third_party_libs.tgz /
ADD https://github.com/go-task/task/releases/download/v3.10.0/task_linux_amd64.deb /
RUN dpkg -i /task_linux_amd64.deb && rm /task_linux_amd64.deb