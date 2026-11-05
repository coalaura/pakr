#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <unistd.h>

/* Spawn from a small, freshly executed process so wait4 does not count the
 * Python parent's resident corpus as part of the codec's peak memory. */
int main(int argc, char **argv) {
	if (argc < 3) {
		return 2;
	}

	pid_t child = fork();
	if (child < 0) {
		return 2;
	}
	if (child == 0) {
		execvp(argv[2], argv + 2);
		_exit(127);
	}

	int status;
	struct rusage usage;
	pid_t result;
	do {
		result = wait4(child, &status, 0, &usage);
	} while (result < 0 && errno == EINTR);
	if (result < 0) {
		return 2;
	}

	FILE *metrics = fopen(argv[1], "w");
	if (metrics == NULL) {
		return 2;
	}
	fprintf(metrics, "%ld\n", usage.ru_maxrss);
	if (fclose(metrics) != 0) {
		return 2;
	}

	return WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
}
