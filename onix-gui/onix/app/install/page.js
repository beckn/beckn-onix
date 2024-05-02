"use client";

import Link from "next/link";
import styles from "../page.module.css";
import { Ubuntu_Mono } from "next/font/google";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          <p className={styles.mainHeading}>ONIX</p>
          <p className={styles.mainText}>
            Open Network In A Box, is a project designed to effortlessly set up
            and maintain Beckn network that is scalable, secure and easy to
            maintain.
          </p>
          <div className={styles.boxesContainer}>
            <Link
              href="/install/join"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Join an existing network</p>
              </div>
            </Link>
            <Link
              href="/install/create"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Create new production network</p>
              </div>
            </Link>
            <Link
              href="/install/local"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>
                  Set up a network on your local machine
                </p>
              </div>
            </Link>
            {/* <Link
              href="/install/merge"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Merge multiple networks</p>
              </div>
            </Link>
            <Link
              href="/install/configure"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Configure Existing Network</p>
              </div>
            </Link> */}
          </div>
        </div>
      </main>
    </>
  );
}
