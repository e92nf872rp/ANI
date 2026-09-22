# Console static bundle. Build from the repository root:
#   make -C repo image-console VERSION=<immutable-tag>
FROM node:22.14.0-alpine3.21@sha256:9bef0ef1e268f60627da9ba7d7605e8831d5b56ad07487d24d1aa386336d1944 AS build

WORKDIR /src/frontends/console

# Keep dependency installation cacheable and reproducible from the committed lockfile.
COPY frontends/console/package.json frontends/console/package-lock.json ./
RUN npm ci

COPY frontends/console/ ./
RUN npm run build

FROM nginx:1.27.5-alpine3.21@sha256:62223d644fa234c3a1cc785ee14242ec47a77364226f1c811d2f669f96dc2ac8

COPY deploy/nginx/default.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/frontends/console/dist /usr/share/nginx/html

EXPOSE 80
