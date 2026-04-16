# spectarr
Automatically monitor movies that your friends on Specto rates over a certain threshold.

## Docker compose
```yaml
  spectarr:
    image: ghcr.io/larsellefsen/spectarr:latest
    container_name: spectarr
    restart: unless-stopped
    user: 1000:1000
    ports:
      - "6969:6969"
    volumes:
      - ./etc/spectarr:/config
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Oslo
```
