# The image of the L2 keygen Job (ops/m4t-tripwires/l2/job.yaml): Python and two files, nothing else.
# Build from the repository root, with the base image pinned by its digest:
#
#   docker build -f ops/m4t-tripwires/l2/Dockerfile --build-arg BASE=python:3.13-slim-bookworm@sha256:<digest> -t ark0-m4t-l2-keygen .
#
# The image's own digest (the one the registry or `k3s ctr images ls` gives) is what job.yaml runs and what
# is published. No package is installed: the program needs only Python's standard library and fips204.py.
ARG BASE
FROM ${BASE}
COPY test/py/core_m1/fips204.py ops/m4t-tripwires/l2/l2-keygen.py /app/
USER 65534:65534
ENV PYTHONDONTWRITEBYTECODE=1
ENTRYPOINT ["python3", "-I", "-B", "/app/l2-keygen.py"]
