#include <pcap.h>
#include <pthread.h>
#include <stdio.h>

#include <arpa/inet.h>

#include <stdint.h>

#include <errno.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/uio.h>
#include <sys/un.h>

#include <unistd.h>

int sock_fd;

int done = 0;

struct __attribute__((__packed__)) pack_header {
	uint32_t data_length;
};

static int
write_iov_count(int fd, struct iovec *iov, size_t count) {
	while (count > 0) {
		ssize_t written = writev(fd, iov, count);
		if (written < 0) {
			if (errno != EAGAIN && errno != EWOULDBLOCK)
				return -1;
			usleep(1000);
			continue;
		}
		/// Adjust iov
		while (written > 0) {
			if (iov->iov_len <=
			    (size_t)written) /// Vec was consumed
			{
				written -= iov->iov_len;
				++iov;
				--count;
				continue;
			}
			iov->iov_base =
				(void *)((intptr_t)iov->iov_base + written);
			iov->iov_len -= written;
			written = 0;
		}
	}
	return 1;
}

static void *
write_thread(void *arg) {
	(void)arg;
	char pcap_errbuf[PCAP_ERRBUF_SIZE];
	struct pcap *pcap = pcap_fopen_offline(stdin, pcap_errbuf);
	if (!pcap) {
		fprintf(stderr, "pcap_fopen_offline(): %s\n", pcap_errbuf);
		done = 1;
		return NULL;
	}

	struct pcap_pkthdr *header;
	const u_char *data;
	static u_char zeros[8192];

	while (pcap_next_ex(pcap, &header, &data) >= 0 && !done) {

		struct pack_header hdr;
		hdr.data_length = htonl(header->len);

		struct iovec iov[3];
		size_t iov_count = 2;

		iov[0].iov_base = &hdr;
		iov[0].iov_len = sizeof(hdr);

		iov[1].iov_base = (void *)data;
		iov[1].iov_len = header->caplen;

		if (header->caplen < header->len) {
			iov[2].iov_base = (void *)zeros;
			iov[2].iov_len = header->len - header->caplen;
			iov_count = 3;
		}

		if (write_iov_count(sock_fd, iov, iov_count) < 0) {
			break;
		}
	}

	pcap_close(pcap);

	usleep(1e6);
	done = 1;

	return NULL;
}

int
read_data(int fd, u_char *buf, ssize_t len) {
	ssize_t ret = 0;
	while (len > 0 && !done) {
		ret = read(fd, buf, len);
		switch (ret) {
		case 0:
			return -1;
		case -1:
			if ((errno == EAGAIN) || (errno = EWOULDBLOCK)) {
				usleep(1000);
			} else {
				return -1;
			}
			break;
		default:
			len -= ret;
			buf += ret;
			break;
		}
	}

	return 0;
}

static int
read_packet(int fd, struct pcap_pkthdr *header, u_char *data) {
	struct pack_header hdr;
	if (read_data(fd, (u_char *)&hdr, sizeof(hdr))) {
		return -1;
	}

	hdr.data_length = ntohl(hdr.data_length);

	if (hdr.data_length == 0) {
		return -1;
	}

	if (read_data(fd, data, hdr.data_length)) {
		return -1;
	}

	header->len = hdr.data_length;
	header->caplen = header->len;
	return 0;
}

static void *
read_thread(void *arg) {
	(void)arg;

	struct pcap *pcap = pcap_open_dead(DLT_EN10MB, 8192);
	struct pcap_dumper *dmp = pcap_dump_fopen(pcap, stdout);

	u_char buffer[8192];
	struct pcap_pkthdr tmp_pcap_packet_header;

	for (; !done;) {
		if (read_packet(sock_fd, &tmp_pcap_packet_header, buffer)) {
			break;
		}

		pcap_dump(
			(unsigned char *)dmp, &tmp_pcap_packet_header, buffer
		);
	}

	done = 1;

	pcap_dump_close(dmp);
	pcap_close(pcap);
	return NULL;
}

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: %s <socket_path>\n", argv[0]);
		return -1;
	}

	sock_fd = socket(AF_UNIX, SOCK_STREAM | SOCK_NONBLOCK, 0);
	if (sock_fd < 0) {
		fprintf(stderr, "could not create socket: %s\n", strerror(errno)
		);
		return -1;
	}
	struct sockaddr_un sockaddr;
	sockaddr.sun_family = AF_UNIX;
	strncpy(sockaddr.sun_path, argv[1], sizeof(sockaddr.sun_path) - 1);
	if (connect(sock_fd, (struct sockaddr *)&sockaddr, sizeof(sockaddr)) <
	    0) {
		fprintf(stderr, "could not connect: %s\n", strerror(errno));
		return -1;
	}

	pthread_t write_th;
	pthread_attr_t write_th_attr;
	pthread_attr_init(&write_th_attr);
	pthread_create(&write_th, &write_th_attr, write_thread, NULL);
	pthread_attr_destroy(&write_th_attr);

	pthread_t read_th;
	pthread_attr_t read_th_attr;
	pthread_attr_init(&read_th_attr);
	pthread_create(&read_th, &read_th_attr, read_thread, NULL);
	pthread_attr_destroy(&read_th_attr);

	pthread_join(write_th, NULL);
	pthread_join(read_th, NULL);

	return 0;
}
