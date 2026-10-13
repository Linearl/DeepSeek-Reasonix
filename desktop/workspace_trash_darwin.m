#import <Foundation/Foundation.h>
#include <stdlib.h>

// Moves one path to the macOS Trash via NSFileManager trashItemAtURL.
// Returns 0 on success; -1 with a strdup'd message otherwise.
int reasonix_trash_path(const char *path, char **error_message) {
	@autoreleasepool {
		if (error_message) { *error_message = NULL; }
		NSString *p = [NSString stringWithUTF8String:path];
		if (p.length == 0) {
			if (error_message) { *error_message = strdup("invalid path"); }
			return -1;
		}
		NSURL *url = [NSURL fileURLWithPath:p];
		NSError *err = nil;
		if (![[NSFileManager defaultManager] trashItemAtURL:url resultingURL:NULL error:&err]) {
			if (error_message) {
				NSString *msg = err.localizedDescription ?: @"trashItemAtURL failed";
				*error_message = strdup(msg.UTF8String);
			}
			return -1;
		}
		return 0;
	}
}
