import Link from "next/link";
import styles from "../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";

const ubuntuMono = Ubuntu_Mono({
    weight: "400",
    style: "normal",
    subsets: ["latin"],
});


export default function Option() {
    return (
        <>
            <main className={ubuntuMono.className}>
                <div className={styles.mainContainer}>
                    <p className={styles.mainText}><b>Yaml File Generator</b></p>
                    <div className={styles.boxesContainer}>
                        <Link
                            href="./option/bap"
                            style={{ textDecoration: "underline", color: "white" }}
                        >
                            <div className={styles.box}>
                                <img src="/arrow.png" />
                                <p className={styles.boxText}>BAP</p>
                            </div>
                        </Link>
                        <Link
                            href="./option/bpp"
                            style={{ textDecoration: "underline", color: "white" }}
                        >
                            <div className={styles.box}>
                                <img src="/arrow.png" />
                                <p className={styles.boxText}>BPP</p>
                            </div>
                        </Link>
                    </div>

                </div>
            </main>
        </>
    )
}